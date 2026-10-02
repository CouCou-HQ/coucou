-- +goose Up
-- A person's /play count across every guild has no guild to narrow by, and plays had no index on
-- user_id, so /stats user scope:global read the whole table. Partial: nothing else looks plays up
-- by user.
create index plays_user_command on stats.plays (user_id) where trigger = 'command';

-- +goose Down
drop index stats.plays_user_command;
