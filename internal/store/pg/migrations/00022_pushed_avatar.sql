-- +goose Up
-- A hash of the avatar last uploaded to the guild, so a restart or a reload pushes only where the
-- character's avatar changed. Null is nothing uploaded: the guild shows the bot's own avatar.
alter table guilds.settings add column pushed_avatar text;

-- +goose Down
alter table guilds.settings drop column pushed_avatar;
