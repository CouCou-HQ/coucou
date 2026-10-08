-- sqlc infers a scalar subquery as NOT NULL, which would make it scan a real NULL into a float64 and
-- fail on an empty guild. Each optional value therefore lives in its own CTE that produces zero rows
-- when there is nothing to report, and is LEFT JOINed in — sqlc marks left-joined columns nullable,
-- so the generated types are the pointers the Store interface promises. Still one round-trip.

-- name: GuildStats :one
with base as (
  select count(*)::int                                                    as plays_all,
         count(*) filter (where a.at > now() - interval '7 days')::int     as plays_7d
  from stats.plays a where a.guild_id = $1
), rate as (
  select avg((not b.ok)::int)::float8 as fail_rate_7d
  from stats.plays b where b.guild_id = $1 and b.at > now() - interval '7 days'
    and b.reason is distinct from 'fakeout'
  having count(*) > 0
), top as (
  select c.sound as top_sound from stats.plays c where c.guild_id = $1 and c.ok
  group by c.sound order by count(*) desc limit 1
), loud as (
  select extract(hour from d.at)::int as loudest_hour from stats.plays d where d.guild_id = $1 and d.ok
  group by 1 order by count(*) desc limit 1
), listen as (
  select avg(e.listeners)::float8 as avg_listeners from stats.plays e where e.guild_id = $1 and e.ok
  having count(*) > 0
)
select base.plays_all, base.plays_7d, rate.fail_rate_7d, top.top_sound, loud.loudest_hour, listen.avg_listeners
from base
left join rate   on true
left join top    on true
left join loud   on true
left join listen on true;

-- name: GlobalStats :one
with base as (
  select count(*) filter (where a.at > now() - interval '24 hours')::int                 as plays_24h,
         count(distinct a.guild_id) filter (where a.at > now() - interval '24 hours')::int as guilds_24h
  from stats.plays a
), top as (
  select c.sound as top_sound from stats.plays c where c.ok and c.at > now() - interval '7 days'
  group by c.sound order by count(*) desc limit 1
)
select base.plays_24h, base.guilds_24h, top.top_sound
from base
left join top on true;

-- name: UserStats :one
-- Same shape as GuildStats: last_heard lives in its own CTE that produces no row until the user
-- has been in earshot once, so sqlc types it as the pointer the Store interface promises. A null
-- guild_id counts every guild.
with heard as (
  select count(*)::int as heard
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where (sqlc.narg(guild_id)::bigint is null or p.guild_id = sqlc.narg(guild_id)) and l.user_id = sqlc.arg(user_id) and p.ok
), triggered as (
  select count(*)::int as triggered
  from stats.plays p
  where (sqlc.narg(guild_id)::bigint is null or p.guild_id = sqlc.narg(guild_id)) and p.user_id = sqlc.arg(user_id)
    and p.trigger = 'command' and p.ok
), recent as (
  select p.at as last_heard
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where (sqlc.narg(guild_id)::bigint is null or p.guild_id = sqlc.narg(guild_id)) and l.user_id = sqlc.arg(user_id) and p.ok
  order by p.at desc limit 1
), fled as (
  select count(*)::int as fled
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where (sqlc.narg(guild_id)::bigint is null or p.guild_id = sqlc.narg(guild_id)) and l.user_id = sqlc.arg(user_id) and l.fled
)
select heard.heard, triggered.triggered, fled.fled, recent.last_heard
from heard
left join triggered on true
left join fled      on true
left join recent    on true;

-- name: HeardSounds :many
-- Names only: the collection is measured against the live registry, which only Go has. A null
-- guild_id collects across every guild.
select distinct p.sound
from stats.play_listeners l join stats.plays p on p.id = l.play_id
where (sqlc.narg(guild_id)::bigint is null or p.guild_id = sqlc.narg(guild_id)) and l.user_id = sqlc.arg(user_id) and p.ok
  and p.trigger = any(sqlc.arg(triggers)::text[]);

-- Leaderboards. days = 0 means all time. Every query returns (key text, n int) so callers share one shape.

-- name: BoardHeard :many
select l.user_id::text as key, count(*)::int as n
from stats.play_listeners l join stats.plays p on p.id = l.play_id
where p.guild_id = $1 and p.ok
  and (sqlc.arg(days)::int = 0 or p.at > now() - make_interval(days => sqlc.arg(days)::int))
group by l.user_id order by n desc limit 10;

-- name: BoardFled :many
select l.user_id::text as key, count(*)::int as n
from stats.play_listeners l join stats.plays p on p.id = l.play_id
where p.guild_id = $1 and l.fled
  and (sqlc.arg(days)::int = 0 or p.at > now() - make_interval(days => sqlc.arg(days)::int))
group by l.user_id order by n desc limit 10;

-- name: BoardTriggered :many
select p.user_id::text as key, count(*)::int as n
from stats.plays p
where p.guild_id = $1 and p.trigger = 'command' and p.ok and p.user_id is not null
  and (sqlc.arg(days)::int = 0 or p.at > now() - make_interval(days => sqlc.arg(days)::int))
group by p.user_id order by n desc limit 10;

-- name: BoardSounds :many
select p.sound as key, count(*)::int as n
from stats.plays p
where p.guild_id = $1 and p.ok
  and (sqlc.arg(days)::int = 0 or p.at > now() - make_interval(days => sqlc.arg(days)::int))
group by p.sound order by n desc limit 10;

-- name: BoardChannels :many
select p.channel_id::text as key, count(*)::int as n
from stats.plays p
where p.guild_id = $1 and p.ok
  and (sqlc.arg(days)::int = 0 or p.at > now() - make_interval(days => sqlc.arg(days)::int))
group by p.channel_id order by n desc limit 10;

-- name: TopGuilds :many
select guild_id::text as key, count(*)::int as n
from stats.plays
where ok
  and (sqlc.arg(days)::int = 0 or at > now() - make_interval(days => sqlc.arg(days)::int))
group by guild_id order by n desc limit 10;

-- name: CharacterPlays :many
-- ok plays by who played them, in one guild or, for guild 0, every guild. '' is a play from before
-- characters, which the caller counts as its default.
select coalesce(character, '') as key, count(*)::int as n
from stats.plays
where ok and (sqlc.arg(guild_id)::bigint = 0 or guild_id = sqlc.arg(guild_id)::bigint)
group by coalesce(character, '') order by n desc;

-- Ranks. A person's population is everyone caught at least once in the window; a guild's is every
-- guild with a play in it. Both are counted on ok plays, like heard.

-- name: UserRank :one
-- Exact, on read: a guild's people are a small set. rank() - 1 is how many sit strictly below.
with pop as (
  select l.user_id,
         count(*) filter (where p.ok)::int as heard,
         count(*) filter (where l.fled)::int as fled
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where p.guild_id = sqlc.arg(guild_id) and p.at >= sqlc.arg(since)::timestamptz
  group by l.user_id having count(*) filter (where p.ok) > 0
), asked as (
  select p.user_id, count(*)::int as triggered
  from stats.plays p
  where p.guild_id = sqlc.arg(guild_id) and p.at >= sqlc.arg(since)::timestamptz
    and p.trigger = 'command' and p.ok and p.user_id is not null
  group by p.user_id
), ranked as (
  select pop.user_id, pop.heard, coalesce(asked.triggered, 0)::int as triggered, pop.fled,
         (rank() over (order by pop.heard) - 1)::int                        as heard_below,
         (rank() over (order by coalesce(asked.triggered, 0)) - 1)::int     as triggered_below,
         (rank() over (order by pop.fled) - 1)::int                         as fled_below,
         (count(*) over ())::int                                            as total
  from pop left join asked on asked.user_id = pop.user_id
)
select ranked.heard, ranked.triggered, ranked.fled, ranked.heard_below, ranked.triggered_below, ranked.fled_below, ranked.total
from ranked where ranked.user_id = sqlc.arg(user_id)::bigint;

-- name: UserRecent :one
with heard as (
  select count(*) filter (where p.ok)::int as heard, count(*) filter (where l.fled)::int as fled
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where l.user_id = sqlc.arg(user_id) and p.at >= sqlc.arg(since)::timestamptz
), asked as (
  select count(*)::int as triggered
  from stats.plays p
  where p.user_id = sqlc.arg(user_id) and p.trigger = 'command' and p.ok and p.at >= sqlc.arg(since)::timestamptz
)
select heard.heard, asked.triggered, heard.fled from heard cross join asked;

-- name: GuildRecent :one
select count(*)::int as plays, coalesce(avg(p.listeners), 0)::float8 as avg_listeners
from stats.plays p
where p.guild_id = sqlc.arg(guild_id) and p.ok and p.at >= sqlc.arg(since)::timestamptz;

-- Cut-points, one query per metric. cut k (1-100) is the value at sorted position ceil(k*N/100):
-- the smallest value at least k% of the population is at or under, so at least k% sit strictly
-- below v exactly when cut k < v. An empty population returns no rows. The positions are computed
-- before the join so it can hash on rn rather than test every row against every k.

-- name: CutsHeard :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select count(*)::float8 as n
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where p.ok and p.at >= sqlc.arg(since)::timestamptz and p.at < sqlc.arg(until)::timestamptz
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
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where p.ok and p.at >= sqlc.arg(since)::timestamptz and p.at < sqlc.arg(until)::timestamptz
), pop as (
  select count(a.id)::float8 as n
  from caught c left join stats.plays a on a.user_id = c.user_id and a.trigger = 'command' and a.ok
    and a.at >= sqlc.arg(since)::timestamptz and a.at < sqlc.arg(until)::timestamptz
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
  select (count(*) filter (where l.fled))::float8 as n
  from stats.play_listeners l join stats.plays p on p.id = l.play_id
  where p.at >= sqlc.arg(since)::timestamptz and p.at < sqlc.arg(until)::timestamptz
  group by l.user_id having count(*) filter (where p.ok) > 0
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- name: CutsPlays :many
with recursive k as (select 1 as k union all select k.k + 1 from k where k.k < 100),
pop as (
  select count(*)::float8 as n
  from stats.plays p
  where p.ok and p.at >= sqlc.arg(since)::timestamptz and p.at < sqlc.arg(until)::timestamptz
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
  select avg(p.listeners)::float8 as n
  from stats.plays p
  where p.ok and p.at >= sqlc.arg(since)::timestamptz and p.at < sqlc.arg(until)::timestamptz
  group by p.guild_id
), v as (
  select pop.n, row_number() over (order by pop.n) as rn from pop
), pos as (
  select k.k, (k.k * (select count(*) from pop) + 99) / 100 as rn from k
)
select v.n from pos join v on v.rn = pos.rn order by pos.k;

-- Hourly series, read through the views that join the rollups to their live tail (00017). Hours
-- come back in UTC and only where something happened; the zone a report is drawn in, and the
-- empty hours between, are Go's.

-- name: PlaysHourly :many
-- A null guild_id is the whole bot, and guilds is then how many servers heard something that hour.
select h.hour::timestamptz          as hour,
       sum(h.plays)::int            as plays,
       sum(h.failed)::int           as failed,
       sum(h.fakeouts)::int         as fakeouts,
       sum(h.loops)::int            as loops,
       sum(h.commands)::int         as commands,
       sum(h.encores)::int          as encores,
       sum(h.listeners)::int        as listeners,
       (count(*) filter (where h.plays > 0))::int as guilds
from stats.plays_hourly h
where (sqlc.narg(guild_id)::bigint is null or h.guild_id = sqlc.narg(guild_id))
  and h.hour >= date_trunc('hour', sqlc.arg(since)::timestamptz, 'UTC')
group by h.hour
order by h.hour;

-- name: UserHourly :many
select h.hour::timestamptz   as hour,
       sum(h.heard)::int     as heard,
       sum(h.fled)::int      as fled,
       sum(h.triggered)::int as triggered
from stats.listeners_hourly h
where h.user_id = sqlc.arg(user_id)
  and (sqlc.narg(guild_id)::bigint is null or h.guild_id = sqlc.narg(guild_id))
  and h.hour >= date_trunc('hour', sqlc.arg(since)::timestamptz, 'UTC')
group by h.hour
order by h.hour;
