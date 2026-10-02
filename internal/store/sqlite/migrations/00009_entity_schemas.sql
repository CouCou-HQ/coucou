-- +goose Up
-- Follows the postgres backend's move from discord and coucou to guilds and users; the schema is
-- still a prefix here, for the reason 00007 gives.
alter table discord_guilds  rename to guilds_info;
alter table coucou_settings rename to guilds_settings;
alter table coucou_optouts  rename to users_optouts;

-- +goose Down
alter table users_optouts   rename to coucou_optouts;
alter table guilds_settings rename to coucou_settings;
alter table guilds_info     rename to discord_guilds;
