-- +goose Up
-- See the postgres 00022 migration.
alter table guilds_settings add column pushed_avatar text;

-- +goose Down
alter table guilds_settings drop column pushed_avatar;
