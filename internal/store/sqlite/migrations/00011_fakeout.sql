-- +goose Up
-- See the postgres 00012 migration.
alter table guilds_settings add column fakeout integer not null default 0 check (fakeout between 0 and 50);

-- +goose Down
alter table guilds_settings drop column fakeout;
