-- +goose Up
-- Bot-wide rather than per guild: the two deployments already have separate databases, so opting
-- out here is opting out of this bot entirely. Unbounded across the bot, unlike the per-guild
-- tables above — one row per user who asked, and no row at all for everyone else.
create table optouts (
  user_id bigint primary key,
  since   timestamptz not null default now()
);

-- +goose Down
drop table optouts;
