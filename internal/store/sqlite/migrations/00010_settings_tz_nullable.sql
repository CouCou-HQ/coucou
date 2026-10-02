-- +goose Up
-- See the postgres 00011 migration. sqlite cannot drop a NOT NULL or a default with ALTER, so this
-- is the documented rebuild, as in 00003.
create table guilds_settings_new (
  guild_id    integer primary key,
  join_chance integer not null default 0 check (join_chance between 0 and 100),
  quiet_from  integer check (quiet_from between 0 and 23),
  quiet_to    integer check (quiet_to between 0 and 23),
  tz          text,
  suspense    integer not null default 0 check (suspense between 0 and 20),
  updated_at  text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_by  integer
);
insert into guilds_settings_new (guild_id, join_chance, quiet_from, quiet_to, tz, suspense, updated_at, updated_by)
  select guild_id, join_chance, quiet_from, quiet_to, tz, suspense, updated_at, updated_by from guilds_settings;
drop table guilds_settings;
alter table guilds_settings_new rename to guilds_settings;

-- +goose Down
create table guilds_settings_old (
  guild_id    integer primary key,
  join_chance integer not null default 0 check (join_chance between 0 and 100),
  quiet_from  integer check (quiet_from between 0 and 23),
  quiet_to    integer check (quiet_to between 0 and 23),
  tz          text not null default 'Europe/Brussels',
  suspense    integer not null default 0 check (suspense between 0 and 20),
  updated_at  text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_by  integer
);
insert into guilds_settings_old (guild_id, join_chance, quiet_from, quiet_to, tz, suspense, updated_at, updated_by)
  select guild_id, join_chance, quiet_from, quiet_to, coalesce(tz, 'Europe/Brussels'), suspense, updated_at, updated_by
  from guilds_settings;
drop table guilds_settings;
alter table guilds_settings_old rename to guilds_settings;
