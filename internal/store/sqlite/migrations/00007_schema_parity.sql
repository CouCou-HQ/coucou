-- +goose Up
-- The names line up with the postgres backend, where the same tables now live in discord, coucou,
-- stats and audit schemas. SQLite has one namespace — its ATTACH "schemas" are separate files, not a
-- grouping inside one database — so the schema becomes a prefix. The point is that the two backends
-- read the same when held side by side, not that SQLite has gained anything.
alter table guilds         rename to discord_guilds;
alter table guild_settings rename to coucou_settings;
alter table optouts        rename to coucou_optouts;
alter table plays          rename to stats_plays;
alter table play_listeners rename to stats_play_listeners;
alter table events         rename to audit_events;

-- Who asked for the change. Postgres has this for its audit trigger to copy; here nothing reads it
-- yet, but without the column the actor the store is handed is silently thrown away.
--
-- There is no audit_logs table and no trigger to go with it. Writing one means enumerating every
-- column of every audited table by hand: SQLite has no to_jsonb(NEW), so a trigger cannot ask a row
-- what its columns are. That trigger would then go stale, silently, the first time a column is added
-- — which is the exact failure the postgres trigger exists to make impossible.
alter table coucou_settings add column updated_by integer;

-- +goose Down
alter table coucou_settings drop column updated_by;
alter table audit_events         rename to events;
alter table stats_play_listeners rename to play_listeners;
alter table stats_plays          rename to plays;
alter table coucou_optouts       rename to optouts;
alter table coucou_settings      rename to guild_settings;
alter table discord_guilds       rename to guilds;
