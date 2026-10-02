-- +goose Up
-- See the postgres 00018 migration.
update stats_plays
set sound = lower(replace(replace(sound, ' ', '_'), '-', '_'))
where sound <> lower(replace(replace(sound, ' ', '_'), '-', '_'));

update audit_events
set data = json_set(data, '$.name', lower(replace(replace(json_extract(data, '$.name'), ' ', '_'), '-', '_')))
where kind in ('sound_added', 'sound_removed')
  and json_extract(data, '$.name') <> lower(replace(replace(json_extract(data, '$.name'), ' ', '_'), '-', '_'));

-- +goose Down
select 1;
