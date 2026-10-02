-- name: ListChaos :many
-- Read once at boot, like the settings. A guild whose newest row is an off has no window at all.
select guild_id, rrule, hours, chance from (
  select distinct on (guild_id) guild_id, rrule, hours, chance
  from guilds.chaos
  order by guild_id, id desc
) latest
where rrule is not null;

-- name: InsertChaos :exec
insert into guilds.chaos (guild_id, rrule, hours, chance, created_by) values ($1, $2, $3, $4, $5);
