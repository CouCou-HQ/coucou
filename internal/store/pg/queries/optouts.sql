-- name: ListOptOuts :many
-- Read once at boot. The set is small (people who actively asked), and every later change comes
-- through SetOptOut/ClearOptOut, so the loop never reads this table.
--
-- Expired rows are filtered here rather than swept by a job: they cost one predicate on a query
-- that runs once per process, and a row nobody will read again is not worth a schedule.
select user_id, until, rrule, window_s from users.optouts where until is null or until > now();

-- name: SetOptOut :exec
-- An upsert rather than the do-nothing it used to be: opting out again is how a timed opt-out gets
-- extended, shortened, or turned into an indefinite one.
insert into users.optouts (user_id, until, rrule, window_s) values ($1, $2, $3, $4)
on conflict (user_id) do update set
  until    = excluded.until,
  rrule    = excluded.rrule,
  window_s = excluded.window_s;

-- name: ClearOptOut :exec
delete from users.optouts where user_id = $1;
