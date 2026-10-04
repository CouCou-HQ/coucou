-- name: ListSettings :many
select guild_id, join_chance, tz, suspense, fakeout, encore, nsfw
from guilds.settings;

-- name: UpsertSettings :exec
-- updated_by is written for the audit trigger to copy into the record, not to be read back. 0 is the
-- bot acting on its own — seeding a guild it just joined, reconciling at boot — and becomes null.
insert into guilds.settings (guild_id, join_chance, tz, suspense, fakeout, encore, nsfw, updated_by)
values ($1, $2, $3, $4, $5, $6, $7, nullif(sqlc.arg(updated_by)::bigint, 0))
on conflict (guild_id) do update set
  join_chance = excluded.join_chance,
  tz          = excluded.tz,
  suspense    = excluded.suspense,
  fakeout     = excluded.fakeout,
  encore      = excluded.encore,
  nsfw        = excluded.nsfw,
  updated_by  = excluded.updated_by,
  updated_at  = now();
