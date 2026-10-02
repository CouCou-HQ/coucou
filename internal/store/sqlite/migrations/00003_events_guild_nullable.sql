-- +goose Up
-- See the postgres 00004 migration. sqlite cannot drop a NOT NULL with ALTER, so this is the
-- documented rebuild: new table, copy across, swap the name, put the index back.
create table events_new (
  id       integer primary key autoincrement,
  at       text not null,
  kind     text not null,
  guild_id integer,
  user_id  integer,
  data     text not null default '{}'
);
insert into events_new (id, at, kind, guild_id, user_id, data)
  select id, at, kind, nullif(guild_id, 0), user_id, data from events;
drop table events;
alter table events_new rename to events;
create index events_kind_at on events (kind, at desc);

-- +goose Down
create table events_old (
  id       integer primary key autoincrement,
  at       text not null,
  kind     text not null,
  guild_id integer not null,
  user_id  integer,
  data     text not null default '{}'
);
insert into events_old (id, at, kind, guild_id, user_id, data)
  select id, at, kind, coalesce(guild_id, 0), user_id, data from events;
drop table events;
alter table events_old rename to events;
create index events_kind_at on events (kind, at desc);
