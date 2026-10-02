-- +goose Up
-- See the postgres 00016 migration.
create index plays_user_command on stats_plays (user_id) where trigger = 'command';

-- +goose Down
drop index plays_user_command;
