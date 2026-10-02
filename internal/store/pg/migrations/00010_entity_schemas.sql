-- +goose Up
-- The tables regroup by what they are about rather than by who owns the truth: a guild's rows in
-- guilds, a user's in users. stats and audit stay as they are — those two are about kinds of record,
-- not about one entity.
--
-- guilds.info is the gateway mirror and guilds.settings what the guild configured, so the restore
-- line 00009 drew now runs between two tables in one schema rather than between two schemas.
--
-- audit.logs rows written before this keep schema_name 'coucou': they record where the table was when
-- the change happened, and rewriting the audit trail to match a rename is the one thing it must not do.
create schema guilds;
create schema users;

alter table discord.guilds set schema guilds;
alter table guilds.guilds rename to info;

alter table coucou.settings set schema guilds;

alter table coucou.optouts set schema users;

drop schema discord;
drop schema coucou;

-- +goose Down
create schema discord;
create schema coucou;
alter table users.optouts   set schema coucou;
alter table guilds.settings set schema coucou;
alter table guilds.info rename to guilds;
alter table guilds.guilds   set schema discord;
drop schema users;
drop schema guilds;
