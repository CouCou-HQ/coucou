-- /forget: a user's id comes out of everything the bot recorded. Rows that are also a server's
-- history (plays it triggered, settings changes it made) keep their place with no one named.

-- name: ForgetListens :exec
delete from stats.play_listeners where user_id = $1;

-- name: ForgetPlays :exec
update stats.plays set user_id = null where user_id = $1;

-- name: ForgetEvents :exec
update audit.events set user_id = null where user_id = $1;

-- name: ForgetActor :exec
update audit.logs set by = null where by = $1;

-- name: ForgetOptOutLog :exec
-- The audit trail of opt-outs from before the silence tables stopped being audited.
delete from audit.logs where table_name = 'optouts' and (pk).id = sqlc.arg(user_id)::bigint;

-- name: ForgetSettingsBy :exec
update guilds.settings set updated_by = null where updated_by = $1;

-- name: ForgetQuietBy :exec
update guilds.quiet set created_by = 0 where created_by = $1;

-- name: ForgetChaosBy :exec
update guilds.chaos set created_by = 0 where created_by = $1;

-- name: ForgetOptOutHistory :exec
-- The live opt-out stays: it is what keeps the bot leaving them out.
delete from users.optouts where user_id = $1 and disabled_at <= now();
