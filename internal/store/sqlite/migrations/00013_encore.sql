-- +goose NO TRANSACTION
-- +goose Up
-- See the postgres 00014 migration. SQLite cannot alter a check constraint, so stats_plays is
-- rebuilt. Foreign keys are off for the swap, which a transaction would silently ignore: with them
-- on, dropping the old table cascades into stats_play_listeners and deletes every row.
pragma foreign_keys = off;
begin;
alter table guilds_settings add column encore integer not null default 0 check (encore between 0 and 50);

create table stats_plays_new (
  id          integer primary key autoincrement,
  at          text not null,
  guild_id    integer not null,
  channel_id  integer not null,
  sound       text not null,
  trigger     text not null check (trigger in ('loop','command','encore')),
  user_id     integer,
  listeners   integer not null,
  ok          integer not null,
  reason      text,
  duration_ms integer not null
);
insert into stats_plays_new select * from stats_plays;
drop table stats_plays;
alter table stats_plays_new rename to stats_plays;
create index plays_guild_at on stats_plays (guild_id, at desc);
create index plays_at on stats_plays (at);
commit;
pragma foreign_keys = on;

-- +goose Down
pragma foreign_keys = off;
begin;
delete from stats_plays where trigger = 'encore';
create table stats_plays_old (
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
insert into stats_plays_old select * from stats_plays;
drop table stats_plays;
alter table stats_plays_old rename to stats_plays;
create index plays_guild_at on stats_plays (guild_id, at desc);
create index plays_at on stats_plays (at);
alter table guilds_settings drop column encore;
commit;
pragma foreign_keys = on;
