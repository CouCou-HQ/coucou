-- +goose Up
-- See the postgres 00021 migration.
alter table guilds_settings add column character text;
alter table stats_plays add column character text;

-- +goose Down
alter table stats_plays drop column character;
alter table guilds_settings drop column character;
