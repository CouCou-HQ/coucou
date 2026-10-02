-- +goose Up
-- Not every recorded event belongs to a guild: a sound appearing in the sounds directory is global.
-- Those were about to be written as guild_id 0, which the first query that groups by guild would
-- count as a real server.
alter table events alter column guild_id drop not null;
update events set guild_id = null where guild_id = 0;

-- +goose Down
-- 0 is the sentinel the column used before it could say "no guild".
update events set guild_id = 0 where guild_id is null;
alter table events alter column guild_id set not null;
