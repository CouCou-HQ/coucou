-- +goose Up
-- The tables split by who owns the truth, which is the question a restore has to answer: what has to
-- be recovered, and what reconnects and refills itself.
--
-- discord is the mirror. guilds is reconciled from the gateway on every boot, so an empty one
-- backfills itself — losing it costs a restart, not data. The first_seen/last_seen/left_at columns
-- are ours rather than Discord's, but they are how the mirror is tracked, not separate facts.
--
-- coucou is what Discord could not tell us again: what a guild configured, and who asked not to be
-- counted. Small, hand-made, and irreplaceable.
--
-- stats is the firehose — one row per sound played and one per listener who heard it. Also
-- irreplaceable, but it grows without bound and is only ever read in aggregate, which is a different
-- thing to back up and a different thing to prune.
--
-- audit already exists and does not move. Its schema_name column stops being the constant 'public'
-- once the audited tables live somewhere worth naming.
create schema discord;
create schema coucou;
create schema stats;

alter table guilds set schema discord;

alter table guild_settings set schema coucou;
-- guild_ was carrying what the schema now says; settings of what else, in a schema called coucou.
alter table coucou.guild_settings rename to settings;

alter table optouts set schema coucou;

alter table plays          set schema stats;
alter table play_listeners set schema stats;

-- +goose Down
alter table stats.play_listeners set schema public;
alter table stats.plays          set schema public;
alter table coucou.optouts       set schema public;
alter table coucou.settings rename to guild_settings;
alter table coucou.guild_settings set schema public;
alter table discord.guilds set schema public;
drop schema stats;
drop schema coucou;
drop schema discord;
