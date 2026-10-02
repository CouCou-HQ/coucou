-- +goose Up
-- A listener fled when they were in the channel as the bot arrived and gone as it left. A failed
-- play now keeps its fled rows too, since everyone fleeing is what empties the room, so heard
-- counts filter on plays.ok rather than on failed plays having no listener rows.
alter table stats.play_listeners add column fled boolean not null default false;

-- +goose Down
delete from stats.play_listeners l using stats.plays p where p.id = l.play_id and not p.ok;
alter table stats.play_listeners drop column fled;
