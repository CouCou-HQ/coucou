-- +goose Up
-- See the postgres 00020 migration.
alter table guilds_settings
  add column nsfw text not null default 'restricted' check (nsfw in ('off','restricted','on'));

-- +goose Down
alter table guilds_settings drop column nsfw;
