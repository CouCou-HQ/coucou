-- name: UpsertGuilds :exec
-- Batch upsert from the gateway's guild set. A guild we thought we'd left but see again gets left_at cleared.
insert into guilds.info (guild_id, joined_at)
select
  unnest(sqlc.arg(guild_ids)::bigint[]),
  unnest(sqlc.arg(joined_ats)::timestamptz[])
on conflict (guild_id) do update set
  joined_at = excluded.joined_at,
  last_seen = now(),
  left_at   = null;

-- name: MarkGuildsLeftExcept :many
-- Anything still marked present that is NOT in the given set was left while we were down.
update guilds.info set left_at = now()
where left_at is null and guild_id <> all(sqlc.arg(present)::bigint[])
returning guild_id;

-- name: MarkGuildLeft :exec
update guilds.info set left_at = now() where guild_id = $1;

-- name: SeedSettingsForGuilds :execrows
-- Give every present guild a settings row with the bot's default chance if it doesn't have one yet.
-- ON CONFLICT DO NOTHING means guilds that already configured themselves are untouched.
insert into guilds.settings (guild_id, join_chance)
select guild_id, sqlc.arg(default_chance)::smallint from guilds.info where left_at is null
on conflict (guild_id) do nothing;

-- name: SeedSettingsForGuild :exec
insert into guilds.settings (guild_id, join_chance) values ($1, $2)
on conflict (guild_id) do nothing;
