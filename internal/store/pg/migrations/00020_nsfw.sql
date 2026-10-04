-- +goose Up
-- Where a guild's nsfw sounds may play: off, only in voice channels Discord has labelled
-- age-restricted, or anywhere. restricted is what every guild did before this.
alter table guilds.settings
  add column nsfw text not null default 'restricted' check (nsfw in ('off','restricted','on'));

-- +goose Down
alter table guilds.settings drop column nsfw;
