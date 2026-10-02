-- +goose Up
-- Null is a guild that never chose a zone, resolved at read time from its locale or else UTC. The
-- rows already stored keep their value: nulling the Brussels ones would move every guild that took
-- the old default to UTC and shift quiet hours that are in use.
alter table guilds.settings alter column tz drop not null, alter column tz drop default;

-- +goose Down
update guilds.settings set tz = 'Europe/Brussels' where tz is null;
alter table guilds.settings alter column tz set default 'Europe/Brussels', alter column tz set not null;
