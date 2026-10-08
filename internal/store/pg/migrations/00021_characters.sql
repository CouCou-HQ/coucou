-- +goose Up
-- Which character a guild has, by profile id. Null is the bot's default character, which is what
-- every guild had before a bot could run more than one.
alter table guilds.settings add column character text;

-- Who played: a guild can switch characters, so its plays are not all the same character's. Null is
-- a play from before this column, counted as the default character.
alter table stats.plays add column character text;

-- +goose Down
alter table stats.plays drop column character;
alter table guilds.settings drop column character;
