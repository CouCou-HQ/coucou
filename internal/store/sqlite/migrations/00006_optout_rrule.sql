-- +goose Up
-- See the postgres 00007 migration. Separate statements because sqlite's ALTER TABLE takes one
-- column at a time.
alter table optouts add column rrule text;
alter table optouts add column window_s integer check (window_s is null or window_s > 0);

-- +goose Down
alter table optouts drop column window_s;
alter table optouts drop column rrule;
