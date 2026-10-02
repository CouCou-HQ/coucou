-- +goose Up
-- See the postgres 00003 migration: both columns were write-only and stale by design.
alter table guilds drop column name;
alter table guilds drop column member_count;

-- +goose Down
alter table guilds add column name text not null default '';
alter table guilds add column member_count integer not null default 0;
