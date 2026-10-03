-- +goose Up
-- Sound files are named in snake_case from here on and rendered with capitals and spaces, so a
-- play recorded as "Wet Fart 2" or "big-burp" is the same sound as wet_fart_2.ogg or
-- big_burp.ogg. Without this its history would split across the old and the new name.
update stats.plays
set sound = lower(replace(replace(sound, ' ', '_'), '-', '_'))
where sound <> lower(replace(replace(sound, ' ', '_'), '-', '_'));

-- The registry's own records of a sound appearing and leaving name it the same way.
update audit.events
set data = jsonb_set(data, '{name}', to_jsonb(lower(replace(replace(data->>'name', ' ', '_'), '-', '_'))))
where kind in ('sound_added', 'sound_removed')
  and data->>'name' <> lower(replace(replace(data->>'name', ' ', '_'), '-', '_'));

-- +goose Down
-- Nothing to undo: the old spelling is not recoverable, and the new one is a valid name either way.
select 1;
