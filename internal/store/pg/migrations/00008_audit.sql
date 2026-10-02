-- +goose Up
-- The audit trail moves into its own schema and splits in two, by origin.
--
-- audit.events is what the bot says happened: a command ran, a guild was joined, a sound appeared.
-- None of those is a row changing, so no trigger can see them; the shape is unchanged, the table
-- just moves.
--
-- audit.logs is what the database saw change, written by a trigger rather than from Go. A settings
-- or opt-out change is a consequence of the write, so recording it here cannot be forgotten at a
-- call site or skipped by a hand-run update — and the row carries the values, which the bot's own
-- record never did.
create schema audit;

alter table events set schema audit;

-- Every row change on every audited table, identified the same way whatever the table: where it
-- lives, which row it was, who asked, and what moved.
--
-- schema_name/table_name rather than schema/table because table is a reserved word, and these are
-- the names plpgsql itself uses for them. op is not implied by the rest: a diff from null to a value
-- is what both an insert and an update that filled in empty columns look like.
--
-- Which row it was: a column name and a snowflake, as one value rather than json, because every
-- primary key here is a single snowflake and nothing else. (pk).id compares and joins against the
-- table it points at as a bigint, with no cast and no json operator.
--
-- The check earns its place: a composite whose fields are all null is not itself null, so row(null,
-- null) satisfies not null and stores as an empty key. not null alone guarantees nothing here.
create type audit.key as (column_name text, id bigint);

create table audit.logs (
  id          bigint generated always as identity primary key,
  at          timestamptz not null default now(),
  schema_name text not null,
  table_name  text not null,
  op          text not null check (op in ('insert','update','delete')),
  pk          audit.key not null check ((pk).column_name is not null and (pk).id is not null),
  by          bigint,
  change      jsonb not null
);
create index logs_row_at on audit.logs (table_name, ((pk).id), at desc);

-- Who asked for the change, for the trigger to copy into by. Null is the bot acting on its own:
-- seeding a guild it just joined, reconciling at boot.
alter table guild_settings add column updated_by bigint;

-- +goose StatementBegin
create function audit.record() returns trigger language plpgsql as $$
declare
  o      jsonb := to_jsonb(old);   -- null on insert
  n      jsonb := to_jsonb(new);   -- null on delete
  r      jsonb := coalesce(n, o);
  pk_cols text[];
  pk_col  text;
  change jsonb;
begin
  -- The key column comes from the catalog, not from a trigger argument: auditing another table is
  -- then one create trigger and nothing else, and the key cannot drift from the one the table
  -- actually has. One lookup per changed row, which is free while the audited tables change a few
  -- times a day; a busy table would want this cached.
  --
  select array_agg(a.attname) into pk_cols
  from pg_index i
  cross join lateral unnest(i.indkey::int2[]) as k(attnum)
  join pg_attribute a on a.attrelid = i.indrelid and a.attnum = k.attnum
  where i.indrelid = tg_relid and i.indisprimary;

  -- Refused rather than mangled. select into strict would catch both cases on its own, but its hint
  -- reads "use LIMIT 1", which is the one repair that would silently log a composite key under half
  -- of itself.
  if coalesce(array_length(pk_cols, 1), 0) <> 1 then
    raise exception 'audit.record: %.% has % primary key columns, and audit.logs records one snowflake per row',
      tg_table_schema, tg_table_name, coalesce(array_length(pk_cols, 1), 0);
  end if;
  pk_col := pk_cols[1];

  -- What moved, and only what moved: carrying both whole rows would bury a one-column change under
  -- five columns that stayed put. The key column, updated_at and updated_by are skipped because this
  -- table already has all three, as pk, at and by.
  --
  -- The missing side of an insert or a delete is coalesced to a json null so that it compares equal
  -- to a column that was null anyway: without it every nullable column a new row did not fill would
  -- be reported as having changed from nothing to nothing.
  select jsonb_object_agg(k, jsonb_build_object('from', o -> k, 'to', n -> k)) into change
  from jsonb_object_keys(r) k
  where k not in (pk_col, 'updated_at', 'updated_by')
    and coalesce(o -> k, 'null'::jsonb) is distinct from coalesce(n -> k, 'null'::jsonb);

  -- An upsert that rewrites a row with the values it already had still moves updated_at, and is not
  -- somebody changing the settings. Nothing moved, so there is nothing to record.
  if change is null then
    return null;
  end if;

  insert into audit.logs (schema_name, table_name, op, pk, by, change)
  values (
    tg_table_schema, tg_table_name, lower(tg_op), row(pk_col, (r ->> pk_col)::bigint)::audit.key,
    -- The actor: the column where the row records one, else the row's own user — an opt-out is
    -- always set by the person it is about.
    coalesce((r->>'updated_by')::bigint, (r->>'user_id')::bigint),
    change
  );
  return null;
end $$;
-- +goose StatementEnd

-- Auditing a table is this line and nothing else. guilds deliberately does not get one: UpsertGuilds
-- touches last_seen for every guild on every boot, so it would write a row per guild per restart,
-- and the join and leave records the bot publishes already carry more than the table does.
create trigger audit_log after insert or update or delete on guild_settings
  for each row execute function audit.record();
create trigger audit_log after insert or update or delete on optouts
  for each row execute function audit.record();

-- +goose Down
drop trigger audit_log on optouts;
drop trigger audit_log on guild_settings;
drop function audit.record();
alter table guild_settings drop column updated_by;
drop table audit.logs;
drop type audit.key;
alter table audit.events set schema public;
drop schema audit;
