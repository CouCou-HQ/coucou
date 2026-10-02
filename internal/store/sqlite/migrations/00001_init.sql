-- +goose Up
-- SQLite dialect of the same schema. Timestamps are stored as RFC3339 text (what modernc/sqlite + sqlc
-- do by default for TEXT with time.Time overrides); all "now() - interval" math becomes datetime('now', ...).
create table guild_settings (
  guild_id    integer primary key,
  join_chance integer not null default 0 check (join_chance between 0 and 100),
  quiet_from  integer check (quiet_from between 0 and 23),
  quiet_to    integer check (quiet_to between 0 and 23),
  tz          text not null default 'Europe/Brussels',
  suspense    integer not null default 0 check (suspense between 0 and 20),
  updated_at  text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

create table plays (
  id          integer primary key autoincrement,
  at          text not null,
  guild_id    integer not null,
  channel_id  integer not null,
  sound       text not null,
  trigger     text not null check (trigger in ('loop','command')),
  user_id     integer,
  listeners   integer not null,
  ok          integer not null,
  reason      text,
  duration_ms integer not null
);
create index plays_guild_at on plays (guild_id, at desc);
create index plays_at on plays (at);

create table play_listeners (
  play_id integer not null references plays(id) on delete cascade,
  user_id integer not null,
  primary key (play_id, user_id)
) without rowid;
create index play_listeners_user on play_listeners (user_id);

create table events (
  id       integer primary key autoincrement,
  at       text not null,
  kind     text not null,
  guild_id integer not null,
  user_id  integer,
  data     text not null default '{}'
);
create index events_kind_at on events (kind, at desc);

create table guilds (
  guild_id     integer primary key,
  name         text not null,
  member_count integer not null default 0,
  joined_at    text not null,
  first_seen   text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  last_seen    text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  left_at      text
);
create index guilds_present on guilds (left_at) where left_at is null;

-- +goose Down
drop table guilds; drop table events; drop table play_listeners; drop table plays; drop table guild_settings;
