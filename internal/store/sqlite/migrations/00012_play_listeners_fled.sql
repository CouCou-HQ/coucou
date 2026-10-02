-- +goose Up
-- See the postgres 00013 migration.
alter table stats_play_listeners add column fled integer not null default 0;

-- +goose Down
delete from stats_play_listeners where play_id in (select id from stats_plays where not ok);
alter table stats_play_listeners drop column fled;
