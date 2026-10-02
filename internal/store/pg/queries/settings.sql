-- name: ListSettings :many
select guild_id, join_chance, quiet_from, quiet_to, tz, suspense, fakeout, encore
from guilds.settings;

-- name: UpsertSettings :exec
-- updated_by is written for the audit trigger to copy into the record, not to be read back. 0 is the
-- bot acting on its own — seeding a guild it just joined, reconciling at boot — and becomes null.
insert into guilds.settings (guild_id, join_chance, quiet_from, quiet_to, tz, suspense, fakeout, encore, updated_by)
values ($1, $2, $3, $4, $5, $6, $7, $8, nullif(sqlc.arg(updated_by)::bigint, 0))
on conflict (guild_id) do update set
  join_chance = excluded.join_chance,
  quiet_from  = excluded.quiet_from,
  quiet_to    = excluded.quiet_to,
  tz          = excluded.tz,
  suspense    = excluded.suspense,
  fakeout     = excluded.fakeout,
  encore      = excluded.encore,
  updated_by  = excluded.updated_by,
  updated_at  = now();
