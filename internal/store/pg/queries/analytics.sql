-- The rollups behind stats.plays_hourly and stats.listeners_hourly. Concurrently, so /stats keeps
-- reading the previous rollup while the next one is counted; see 00017.

-- name: RefreshPlaysHourly :exec
refresh materialized view concurrently stats.plays_hourly_mv;

-- name: RefreshListenersHourly :exec
refresh materialized view concurrently stats.listeners_hourly_mv;
