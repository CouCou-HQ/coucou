-- +goose Up
-- The row-change log the postgres backend writes from a trigger. SQLite cannot: a trigger there has
-- no way to ask a row what its columns are — there is no to_jsonb(NEW) — so it would have to name
-- every column by hand and would go stale, silently, the first time one was added. The diff is taken
-- in Go instead, in the same transaction as the write, over the decoded row rather than a list of
-- columns, so a new column appears in the log the day it is added.
--
-- The columns match audit.logs in postgres so the two read alike, except pk: that is one composite
-- audit.key there and two columns here, because SQLite has no composite types.
create table audit_logs (
  id          integer primary key autoincrement,
  at          text not null default (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  schema_name text not null,
  table_name  text not null,
  op          text not null check (op in ('insert','update','delete')),
  pk_column   text not null,
  pk          integer not null,
  by          integer,
  change      text not null
);
create index audit_logs_row_at on audit_logs (table_name, pk, at desc);

-- +goose Down
drop table audit_logs;
