-- name: ListQuiet :many
-- As ListOptOuts, per guild.
select guild_id, rrule, window_s, disabled_at from guilds.quiet
where disabled_at is null or disabled_at > now()
order by id;

-- name: CloseQuiet :exec
update guilds.quiet set disabled_at = now()
where guild_id = $1 and (disabled_at is null or disabled_at > now());

-- name: InsertQuiet :exec
insert into guilds.quiet (guild_id, rrule, window_s, disabled_at, created_by) values ($1, $2, $3, $4, $5);
