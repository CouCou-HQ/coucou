-- +goose Up
-- See the postgres 00019 migration. DTSTART comes from the UTC date here, so two days back covers
-- any zone's yesterday.
create table users_optouts_new (
  id          integer primary key autoincrement,
  user_id     integer not null,
  rrule       text,
  window_s    integer check (window_s > 0),
  created_at  text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  disabled_at text,
  check ((rrule is null) = (window_s is null))
);
insert into users_optouts_new (user_id, rrule, window_s, created_at, disabled_at)
select user_id, rrule, window_s, since, until from users_optouts;
drop table users_optouts;
alter table users_optouts_new rename to users_optouts;
create index optouts_user on users_optouts (user_id);

create table guilds_quiet (
  id          integer primary key autoincrement,
  guild_id    integer not null,
  rrule       text,
  window_s    integer check (window_s > 0),
  created_by  integer not null,
  created_at  text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  disabled_at text,
  check ((rrule is null) = (window_s is null))
);
create index quiet_guild on guilds_quiet (guild_id);

insert into guilds_quiet (guild_id, rrule, window_s, created_by)
select guild_id,
  'DTSTART;TZID=' || coalesce(tz, 'UTC') || ':' || strftime('%Y%m%d', 'now', '-2 days')
    || 'T000000' || char(10) || 'RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYHOUR=' || quiet_from || ';BYMINUTE=0;BYSECOND=0',
  ((quiet_to - quiet_from + 24) % 24) * 3600,
  0
from guilds_settings
where quiet_from is not null and quiet_to is not null and quiet_from <> quiet_to;

alter table guilds_settings drop column quiet_from;
alter table guilds_settings drop column quiet_to;

-- +goose Down
alter table guilds_settings add column quiet_from integer check (quiet_from between 0 and 23);
alter table guilds_settings add column quiet_to integer check (quiet_to between 0 and 23);
drop table guilds_quiet;

create table users_optouts_old (
  user_id  integer primary key,
  since    text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  until    text,
  rrule    text,
  window_s integer check (window_s is null or window_s > 0)
);
insert or replace into users_optouts_old (user_id, since, until, rrule, window_s)
select user_id, created_at, disabled_at, rrule, window_s from users_optouts
where disabled_at is null or disabled_at > strftime('%Y-%m-%dT%H:%M:%fZ','now')
order by id;
drop table users_optouts;
alter table users_optouts_old rename to users_optouts;
