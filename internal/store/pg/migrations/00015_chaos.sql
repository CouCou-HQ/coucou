-- +goose Up
-- A guild's /chaos window: a weekly rule during which the loop rolls against chance instead of
-- /chance. Append-only, and the newest row per guild is the one in effect; /chaos off is a row with
-- no rule. The rule text carries its own DTSTART and TZID, as in users.optouts.
--
-- No audit trigger: every row already is the record of a change, with who made it and when. And
-- audit.record keys a row by its primary key, which here is a row number rather than the guild.
create table guilds.chaos (
  id         bigint generated always as identity primary key,
  guild_id   bigint not null,
  rrule      text,
  hours      smallint check (hours between 1 and 24),
  chance     smallint check (chance between 1 and 100),
  created_by bigint not null,
  created_at timestamptz not null default now(),
  check ((rrule is null) = (hours is null) and (rrule is null) = (chance is null))
);

-- +goose Down
drop table guilds.chaos;
