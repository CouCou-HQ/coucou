-- name: ListOptOuts :many
-- Each user's live row, read once at boot. Oldest first, so a race that left two live rows ends up
-- mirroring the newer one.
select user_id, rrule, window_s, disabled_at from users.optouts
where disabled_at is null or disabled_at > now()
order by id;

-- name: CloseOptOut :exec
update users.optouts set disabled_at = now()
where user_id = $1 and (disabled_at is null or disabled_at > now());

-- name: InsertOptOut :exec
insert into users.optouts (user_id, rrule, window_s, disabled_at) values ($1, $2, $3, $4);
