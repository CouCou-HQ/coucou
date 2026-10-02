-- +goose Up
-- Name and member count were written on every reconcile and read by nothing. A row was only ever as
-- fresh as the last boot, so both were stale within minutes of landing; the live values are in the
-- gateway cache, which is where the two callers that want them already look.
alter table guilds drop column name, drop column member_count;

-- +goose Down
alter table guilds
  add column name text not null default '',
  add column member_count int not null default 0;
