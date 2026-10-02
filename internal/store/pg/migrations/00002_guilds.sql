-- +goose Up
-- Every guild the bot has ever been in. Presence is a row with left_at null; leaving sets left_at.
-- Reconciled from the gateway on every boot, so a fresh database backfills itself.
create table guilds (
  guild_id     bigint primary key,
  name         text not null,
  member_count int not null default 0,
  joined_at    timestamptz not null,       -- when the bot joined (from Discord), not when we first saw it
  first_seen   timestamptz not null default now(),
  last_seen    timestamptz not null default now(),
  left_at      timestamptz
);
create index guilds_present on guilds (left_at) where left_at is null;

-- +goose Down
drop table guilds;
