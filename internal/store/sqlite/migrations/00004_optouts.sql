-- +goose Up
-- See the postgres 00005 migration.
create table optouts (
  user_id integer primary key,
  since   text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- +goose Down
drop table optouts;
