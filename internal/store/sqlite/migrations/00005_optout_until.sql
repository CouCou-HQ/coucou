-- +goose Up
-- See the postgres 00006 migration. RFC3339Nano text, like every other timestamp in this dialect.
alter table optouts add column until text;

-- +goose Down
alter table optouts drop column until;
