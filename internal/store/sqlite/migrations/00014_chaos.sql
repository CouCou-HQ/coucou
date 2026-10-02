-- +goose Up
-- See the postgres 00015 migration. No audit here either: the table is its own history.
create table guilds_chaos (
  id         integer primary key autoincrement,
  guild_id   integer not null,
  rrule      text,
  hours      integer check (hours between 1 and 24),
  chance     integer check (chance between 1 and 100),
  created_by integer not null,
  created_at text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  check ((rrule is null) = (hours is null) and (rrule is null) = (chance is null))
);

-- +goose Down
drop table guilds_chaos;
