-- +goose Up
create table guild_settings (
  guild_id    bigint primary key,
  join_chance smallint not null default 0 check (join_chance between 0 and 100),
  quiet_from  smallint check (quiet_from between 0 and 23),
  quiet_to    smallint check (quiet_to between 0 and 23),
  tz          text not null default 'Europe/Brussels',
  suspense    smallint not null default 0 check (suspense between 0 and 20),
  updated_at  timestamptz not null default now()
);

create table plays (
  id          bigint generated always as identity primary key,
  at          timestamptz not null,
  guild_id    bigint not null,
  channel_id  bigint not null,
  sound       text not null,
  trigger     text not null check (trigger in ('loop','command')),
  user_id     bigint,
  listeners   smallint not null,
  ok          boolean not null,
  reason      text,
  duration_ms int not null
);
create index plays_guild_at on plays (guild_id, at desc);
create index plays_at on plays (at);

create table play_listeners (
  play_id bigint not null references plays(id) on delete cascade,
  user_id bigint not null,
  primary key (play_id, user_id)
);
create index play_listeners_user on play_listeners (user_id);

create table events (
  id       bigint generated always as identity primary key,
  at       timestamptz not null,
  kind     text not null,
  guild_id bigint not null,
  user_id  bigint,
  data     jsonb not null default '{}'
);
create index events_kind_at on events (kind, at desc);

-- +goose Down
drop table events; drop table play_listeners; drop table plays; drop table guild_settings;
