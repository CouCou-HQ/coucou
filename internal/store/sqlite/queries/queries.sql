-- SQLite has no unnest / COPY. Batches are done by the Go side inside one transaction with these
-- single-row statements; sqlite executes ~100k simple inserts/s in a tx, which is plenty.

-- name: ListSettings :many
select guild_id, join_chance, tz, suspense, fakeout, encore, nsfw, character from guilds_settings;

-- name: UpsertSettings :exec
-- updated_by is who asked for the change, null when the bot acted on its own. Postgres nulls the
-- zero in SQL; sqlc's sqlite grammar rejects sqlc.arg() inside nullif(), so actor does it in Go.
-- Nothing reads the column here yet, but dropping it on the floor would make the two backends
-- disagree about what the store was told.
insert into guilds_settings (guild_id, join_chance, tz, suspense, fakeout, encore, nsfw, character, updated_by)
values (?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict (guild_id) do update set
  join_chance = excluded.join_chance, tz = excluded.tz, suspense = excluded.suspense, fakeout = excluded.fakeout, encore = excluded.encore, nsfw = excluded.nsfw, character = excluded.character, updated_by = excluded.updated_by,
  updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now');

-- name: InsertPlay :one
insert into stats_plays (at, guild_id, channel_id, sound, trigger, user_id, listeners, ok, reason, duration_ms, character)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
returning id;

-- name: InsertPlayListener :exec
insert or ignore into stats_play_listeners (play_id, user_id, fled) values (?, ?, ?);

-- name: InsertEvent :exec
insert into audit_events (at, kind, guild_id, user_id, data) values (?, ?, ?, ?, ?);

-- name: UpsertGuild :exec
insert into guilds_info (guild_id, joined_at)
values (?, ?)
on conflict (guild_id) do update set
  joined_at = excluded.joined_at,
  last_seen = strftime('%Y-%m-%dT%H:%M:%fZ','now'), left_at = null;

-- name: PresentGuilds :many
select guild_id from guilds_info where left_at is null;

-- name: MarkGuildLeft :exec
update guilds_info set left_at = strftime('%Y-%m-%dT%H:%M:%fZ','now') where guild_id = ?;

-- name: SeedSettingsForGuilds :execrows
insert or ignore into guilds_settings (guild_id, join_chance, suspense, fakeout, encore)
select guild_id, sqlc.arg(join_chance), sqlc.arg(suspense), sqlc.arg(fakeout), sqlc.arg(encore)
from guilds_info where left_at is null;

-- name: SeedSettingsForGuild :exec
insert or ignore into guilds_settings (guild_id, join_chance, suspense, fakeout, encore) values (?, ?, ?, ?, ?);

-- Same shape as the postgres backend: each optional value gets a CTE that yields zero rows when there
-- is nothing to report and is LEFT JOINed in, so sqlc types them as pointers instead of scanning a
-- real NULL into a float64. sqlc's sqlite grammar has no FILTER clause, so counts use `case when`.

-- name: GuildStats :one
with base as (
  select count(*) as plays_all,
         count(case when a.at > datetime('now','-7 days') then 1 end) as plays_7d
  from stats_plays a where a.guild_id = ?1
), rate as (
  select avg(1 - b.ok) as fail_rate_7d
  from stats_plays b where b.guild_id = ?1 and b.at > datetime('now','-7 days')
    and b.reason is not 'fakeout'
), top as (
  select c.sound as top_sound from stats_plays c where c.guild_id = ?1 and c.ok
  group by c.sound order by count(*) desc limit 1
), loud as (
  select cast(strftime('%H', d.at) as integer) as loudest_hour from stats_plays d where d.guild_id = ?1 and d.ok
  group by 1 order by count(*) desc limit 1
), listen as (
  select avg(e.listeners) as avg_listeners from stats_plays e where e.guild_id = ?1 and e.ok
)
select base.plays_all, base.plays_7d, rate.fail_rate_7d, top.top_sound, loud.loudest_hour, listen.avg_listeners
from base
left join rate   on 1
left join top    on 1
left join loud   on 1
left join listen on 1;

-- name: GlobalStats :one
with base as (
  select count(case when a.at > datetime('now','-24 hours') then 1 end) as plays_24h,
         count(distinct case when a.at > datetime('now','-24 hours') then a.guild_id end) as guilds_24h
  from stats_plays a
), top as (
  select c.sound as top_sound from stats_plays c where c.ok and c.at > datetime('now','-7 days')
  group by c.sound order by count(*) desc limit 1
)
select base.plays_24h, base.guilds_24h, top.top_sound
from base
left join top on 1;

-- Boards take a `since` text param computed in Go ('' = all time), because sqlite can't build an
-- interval from a bound integer inside datetime() the way Postgres' make_interval can.

-- name: UserStats :one
-- A null guild_id counts every guild.
with heard as (
  select count(*) as heard
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and l.user_id = sqlc.arg(user_id) and p.ok
), triggered as (
  select count(*) as triggered
  from stats_plays p
  where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and p.user_id = sqlc.arg(user_id)
    and p.trigger = 'command' and p.ok
), recent as (
  select p.at as last_heard
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and l.user_id = sqlc.arg(user_id) and p.ok
  order by p.at desc limit 1
), fled as (
  select count(*) as fled
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and l.user_id = sqlc.arg(user_id) and l.fled
)
select heard.heard, triggered.triggered, fled.fled, recent.last_heard
from heard
left join triggered on 1
left join fled      on 1
left join recent    on 1;

-- name: HeardSounds :many
select distinct p.sound
from stats_play_listeners l join stats_plays p on p.id = l.play_id
where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and l.user_id = sqlc.arg(user_id) and p.ok
  and p.trigger in (sqlc.slice(triggers));

-- name: BoardHeard :many
select cast(l.user_id as text) as "key", count(*) as n
from stats_play_listeners l join stats_plays p on p.id = l.play_id
where p.guild_id = ?1 and p.ok and (?2 = '' or p.at > ?2)
group by l.user_id order by n desc limit 10;

-- name: BoardFled :many
select cast(l.user_id as text) as "key", count(*) as n
from stats_play_listeners l join stats_plays p on p.id = l.play_id
where p.guild_id = ?1 and l.fled and (?2 = '' or p.at > ?2)
group by l.user_id order by n desc limit 10;

-- name: BoardTriggered :many
select cast(p.user_id as text) as "key", count(*) as n
from stats_plays p
where p.guild_id = ?1 and p.trigger = 'command' and p.ok and p.user_id is not null and (?2 = '' or p.at > ?2)
group by p.user_id order by n desc limit 10;

-- name: BoardSounds :many
select p.sound as "key", count(*) as n
from stats_plays p where p.guild_id = ?1 and p.ok and (?2 = '' or p.at > ?2)
group by p.sound order by n desc limit 10;

-- name: BoardChannels :many
select cast(p.channel_id as text) as "key", count(*) as n
from stats_plays p where p.guild_id = ?1 and p.ok and (?2 = '' or p.at > ?2)
group by p.channel_id order by n desc limit 10;

-- name: TopGuilds :many
select cast(guild_id as text) as "key", count(*) as n
from stats_plays where ok and (?1 = '' or at > ?1)
group by guild_id order by n desc limit 10;

-- Ranks: see the postgres stats.sql. since and until are timestamps formatted in Go, like the
-- boards' since.

-- name: UserRank :one
with pop as (
  select l.user_id, sum(p.ok) as heard, sum(l.fled) as fled
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where p.guild_id = sqlc.arg(guild_id) and p.at >= sqlc.arg(since)
  group by l.user_id having sum(p.ok) > 0
), asked as (
  select p.user_id, count(*) as triggered
  from stats_plays p
  where p.guild_id = sqlc.arg(guild_id) and p.at >= sqlc.arg(since)
    and p.trigger = 'command' and p.ok and p.user_id is not null
  group by p.user_id
), ranked as (
  select pop.user_id, pop.heard, coalesce(asked.triggered, 0) as triggered, pop.fled,
         rank() over (order by pop.heard) - 1                    as heard_below,
         rank() over (order by coalesce(asked.triggered, 0)) - 1 as triggered_below,
         rank() over (order by pop.fled) - 1                     as fled_below,
         count(*) over ()                                        as total
  from pop left join asked on asked.user_id = pop.user_id
)
select cast(ranked.heard as integer) as heard, cast(ranked.triggered as integer) as triggered, cast(ranked.fled as integer) as fled,
       cast(ranked.heard_below as integer) as heard_below, cast(ranked.triggered_below as integer) as triggered_below,
       cast(ranked.fled_below as integer) as fled_below, cast(ranked.total as integer) as total
from ranked where ranked.user_id = cast(sqlc.arg(user_id) as integer);

-- name: UserRecent :one
with heard as (
  select cast(coalesce(sum(p.ok), 0) as integer) as heard, cast(coalesce(sum(l.fled), 0) as integer) as fled
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where l.user_id = sqlc.arg(user_id) and p.at >= sqlc.arg(since)
), asked as (
  select count(*) as triggered
  from stats_plays p
  where p.user_id = sqlc.arg(user_id) and p.trigger = 'command' and p.ok and p.at >= sqlc.arg(since)
)
select heard.heard, asked.triggered, heard.fled from heard cross join asked;

-- name: GuildRecent :one
select count(*) as plays, cast(coalesce(avg(p.listeners), 0) as real) as avg_listeners
from stats_plays p
where p.guild_id = sqlc.arg(guild_id) and p.ok and p.at >= sqlc.arg(since);

-- name: CutsHeard :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select cast(count(*) as real) as n
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where p.ok and p.at >= sqlc.arg(since) and p.at < sqlc.arg(until)
  group by l.user_id
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: CutsTriggered :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
caught as (
  select distinct l.user_id
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where p.ok and p.at >= sqlc.arg(since) and p.at < sqlc.arg(until)
), pop as (
  select cast(count(a.id) as real) as n
  from caught c left join stats_plays a on a.user_id = c.user_id and a.trigger = 'command' and a.ok
    and a.at >= sqlc.arg(since) and a.at < sqlc.arg(until)
  group by c.user_id
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: CutsFled :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select cast(sum(l.fled) as real) as n
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where p.at >= sqlc.arg(since) and p.at < sqlc.arg(until)
  group by l.user_id having sum(p.ok) > 0
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: CutsPlays :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select cast(count(*) as real) as n
  from stats_plays p
  where p.ok and p.at >= sqlc.arg(since) and p.at < sqlc.arg(until)
  group by p.guild_id
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: CutsListeners :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select cast(avg(p.listeners) as real) as n
  from stats_plays p
  where p.ok and p.at >= sqlc.arg(since) and p.at < sqlc.arg(until)
  group by p.guild_id
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: ListOptOuts :many
-- The cutoff comes from Go rather than from strftime so both sides of the comparison carry the one
-- format this dialect writes. RFC3339Nano trims trailing zeros, so the string compare can be off by
-- under a second either way; Has re-checks in memory against a real clock, which is what decides.
select user_id, rrule, window_s, disabled_at from users_optouts
where disabled_at is null or disabled_at > ?1
order by id;

-- name: CloseOptOut :exec
update users_optouts set disabled_at = ?2
where user_id = ?1 and (disabled_at is null or disabled_at > ?2);

-- name: InsertOptOut :exec
insert into users_optouts (user_id, rrule, window_s, disabled_at) values (?, ?, ?, ?);

-- name: ListQuiet :many
select guild_id, rrule, window_s, disabled_at from guilds_quiet
where disabled_at is null or disabled_at > ?1
order by id;

-- name: CloseQuiet :exec
update guilds_quiet set disabled_at = ?2
where guild_id = ?1 and (disabled_at is null or disabled_at > ?2);

-- name: InsertQuiet :exec
insert into guilds_quiet (guild_id, rrule, window_s, disabled_at, created_by) values (?, ?, ?, ?, ?);

-- The audited tables are read back whole, with select *, on purpose: the diff is taken over every
-- column the row has rather than a list written out here, so adding a column does not need this
-- query, the Go side, or anything else edited to keep the log honest.

-- name: GetSettings :one
select * from guilds_settings where guild_id = ?;

-- name: InsertAuditLog :exec
insert into audit_logs (schema_name, table_name, op, pk_column, pk, by, change)
values (?, ?, ?, ?, ?, ?, ?);

-- name: ListChaos :many
select c.guild_id, c.rrule, c.hours, c.chance from guilds_chaos c
where c.rrule is not null and c.id = (select max(l.id) from guilds_chaos l where l.guild_id = c.guild_id);

-- name: InsertChaos :exec
insert into guilds_chaos (guild_id, rrule, hours, chance, created_by) values (?, ?, ?, ?, ?);

-- Hourly series. No rollups on this side: the postgres backend keeps them because it is the one
-- that grows, and a sqlite database small enough to be one file counts a month of hours on read.
-- An hour is the first 13 characters of an RFC3339 UTC timestamp, and since is passed as exactly
-- that prefix: every timestamp inside the hour sorts after it, and every one before sorts below.

-- name: PlaysHourly :many
select cast(substr(p.at, 1, 13) as text)                                                     as hour,
       cast(count(case when p.ok then 1 end) as integer)                                      as plays,
       cast(count(case when not p.ok and coalesce(p.reason, '') != 'fakeout' then 1 end) as integer) as failed,
       cast(count(case when p.reason = 'fakeout' then 1 end) as integer)                      as fakeouts,
       cast(count(case when p.ok and p.trigger = 'loop' then 1 end) as integer)               as loops,
       cast(count(case when p.ok and p.trigger = 'command' then 1 end) as integer)            as commands,
       cast(count(case when p.ok and p.trigger = 'encore' then 1 end) as integer)             as encores,
       cast(coalesce(sum(case when p.ok then p.listeners end), 0) as integer)                 as listeners,
       cast(count(distinct case when p.ok then p.guild_id end) as integer)                    as guilds
from stats_plays p
where (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null) and p.at >= sqlc.arg(since)
group by 1
order by 1;

-- name: UserHourly :many
select cast(x.hour as text) as hour,
       cast(sum(x.heard) as integer) as heard, cast(sum(x.fled) as integer) as fled, cast(sum(x.triggered) as integer) as triggered
from (
  select substr(p.at, 1, 13) as hour, p.ok as heard, l.fled as fled, 0 as triggered
  from stats_play_listeners l join stats_plays p on p.id = l.play_id
  where l.user_id = sqlc.arg(user_id) and (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null)
    and p.at >= sqlc.arg(since)
  union all
  select substr(p.at, 1, 13), 0, 0, 1
  from stats_plays p
  where p.user_id = sqlc.arg(user_id) and (p.guild_id = sqlc.narg(guild_id) or sqlc.narg(guild_id) is null)
    and p.trigger = 'command' and p.ok and p.at >= sqlc.arg(since)
) x
group by x.hour
having sum(x.heard) + sum(x.fled) + sum(x.triggered) > 0
order by x.hour;
