-- +goose Up
-- /optout and /quiet become append-only: a row per change, closed by disabled_at when it is turned
-- off or replaced. No rule is all the time; a rule is its occurrences, each window_s long. A
-- disabled_at still in the future is a timed one's end. No audit trigger, as in guilds.chaos: the
-- rows are their own record.
drop trigger audit_log on users.optouts;

create table users.optouts_new (
  id          bigint generated always as identity primary key,
  user_id     bigint not null,
  rrule       text,
  window_s    integer check (window_s > 0),
  created_at  timestamptz not null default now(),
  disabled_at timestamptz,
  check ((rrule is null) = (window_s is null))
);
insert into users.optouts_new (user_id, rrule, window_s, created_at, disabled_at)
select user_id, rrule, window_s, since, until from users.optouts;
drop table users.optouts;
alter table users.optouts_new rename to optouts;
create index optouts_user on users.optouts (user_id);

create table guilds.quiet (
  id          bigint generated always as identity primary key,
  guild_id    bigint not null,
  rrule       text,
  window_s    integer check (window_s > 0),
  created_by  bigint not null,
  created_at  timestamptz not null default now(),
  disabled_at timestamptz,
  check ((rrule is null) = (window_s is null))
);
create index quiet_guild on guilds.quiet (guild_id);

-- Quiet hours become the rule /quiet schedule writes for "Every day". DTSTART is yesterday so a
-- window that started before midnight is already live.
insert into guilds.quiet (guild_id, rrule, window_s, created_by)
select guild_id,
  'DTSTART;TZID=' || coalesce(tz, 'UTC') || ':'
    || to_char((now() at time zone coalesce(tz, 'UTC')) - interval '1 day', 'YYYYMMDD')
    || E'T000000\nRRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR,SA,SU;BYHOUR=' || quiet_from || ';BYMINUTE=0;BYSECOND=0',
  ((quiet_to - quiet_from + 24) % 24) * 3600,
  0
from guilds.settings
where quiet_from is not null and quiet_to is not null and quiet_from <> quiet_to;

alter table guilds.settings drop column quiet_from, drop column quiet_to;

-- +goose Down
-- Lossy: a quiet rule has no from/to to go back to, and only each user's live opt-out survives.
alter table guilds.settings
  add column quiet_from smallint check (quiet_from between 0 and 23),
  add column quiet_to   smallint check (quiet_to between 0 and 23);
drop table guilds.quiet;

create table users.optouts_old (
  user_id  bigint primary key,
  since    timestamptz not null default now(),
  until    timestamptz,
  rrule    text,
  window_s integer check (window_s is null or window_s > 0)
);
insert into users.optouts_old (user_id, since, until, rrule, window_s)
select distinct on (user_id) user_id, created_at, disabled_at, rrule, window_s
from users.optouts
where disabled_at is null or disabled_at > now()
order by user_id, id desc;
drop table users.optouts;
alter table users.optouts_old rename to optouts;
create trigger audit_log after insert or update or delete on users.optouts
  for each row execute function audit.record();
