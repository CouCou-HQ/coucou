-- +goose Up
-- Analytics, in two layers. The views name the questions — what a listen is, what a play came to,
-- what an hour looked like — so ad-hoc SQL and Grafana ask them the same way the bot does. The
-- materialized views pay for the expensive ones: counting a month of plays per hour on every
-- /stats is the query that grows with the table, so it is rolled up and refreshed on a timer.
--
-- Every rollup is by UTC hour. That is the finest grain any report draws and the coarsest one that
-- still reads back correctly in a guild's own zone: a local day or a weekday-by-hour grid is a sum
-- of UTC hours, and Go does that sum with the zone database it already has.
--
-- The *_hourly views put a rollup together with a live count of every play it has not seen, so a
-- report is exact whatever the refresh timer is doing. "Not seen" is by play id, not by time: the
-- event log re-queues a batch that failed to write, so a play can land long after its own hour, and
-- a watermark on time would skip it until the next refresh. Ids are handed out in insert order and
-- plays have one writer, so everything past the highest id a rollup counted is exactly what it missed.

-- One person in the room for one play. play_listeners carries nothing but the pair; every question
-- about people joins it back to plays for when and where, and this is that join, once.
create view stats.listens as
select l.play_id, p.at, p.guild_id, p.channel_id, p.sound, p.trigger, l.user_id, p.ok, l.fled
from stats.play_listeners l join stats.plays p on p.id = l.play_id;

-- What a play came to. A fake-out is recorded as not ok, and counting it as a failure is how a
-- server with fake-outs on reads as broken — so it is its own outcome rather than a reason to filter.
create view stats.play_outcomes as
select p.id, p.at, p.guild_id, p.channel_id, p.sound, p.trigger, p.user_id, p.listeners, p.reason, p.duration_ms,
       case when p.ok then 'played' when p.reason = 'fakeout' then 'fakeout' else 'failed' end as outcome
from stats.plays p;

create materialized view stats.plays_hourly_mv as
select p.guild_id,
       date_trunc('hour', p.at, 'UTC')                                               as hour,
       count(*) filter (where p.ok)::int                                             as plays,
       count(*) filter (where not p.ok and p.reason is distinct from 'fakeout')::int as failed,
       count(*) filter (where p.reason = 'fakeout')::int                             as fakeouts,
       count(*) filter (where p.ok and p.trigger = 'loop')::int                      as loops,
       count(*) filter (where p.ok and p.trigger = 'command')::int                   as commands,
       count(*) filter (where p.ok and p.trigger = 'encore')::int                    as encores,
       coalesce(sum(p.listeners) filter (where p.ok), 0)::int                        as listeners,
       max(p.id)                                                                     as last_play
from stats.plays p
group by 1, 2;
-- Unique, because refresh ... concurrently needs one to diff against; concurrently, because a plain
-- refresh locks the rollup against every /stats for as long as the recount takes.
create unique index plays_hourly_mv_key on stats.plays_hourly_mv (guild_id, hour);
create index plays_hourly_mv_last_play on stats.plays_hourly_mv (last_play);

-- A person's side of an hour: heard is an ok play they were in the room for, fled is leaving one
-- before it ended, triggered is an ok /play they asked for. The same three counts UserStats makes.
create materialized view stats.listeners_hourly_mv as
select x.guild_id, x.user_id, x.hour,
       sum(x.heard)::int as heard, sum(x.fled)::int as fled, sum(x.triggered)::int as triggered,
       max(x.play_id) as last_play
from (
  select l.play_id, l.guild_id, l.user_id, date_trunc('hour', l.at, 'UTC') as hour,
         l.ok::int as heard, l.fled::int as fled, 0 as triggered
  from stats.listens l
  union all
  select p.id, p.guild_id, p.user_id, date_trunc('hour', p.at, 'UTC'), 0, 0, 1
  from stats.plays p
  where p.trigger = 'command' and p.ok and p.user_id is not null
) x
group by 1, 2, 3
having sum(x.heard) + sum(x.fled) + sum(x.triggered) > 0;
-- User first: every read is about one person, and only sometimes about one guild.
create unique index listeners_hourly_mv_key on stats.listeners_hourly_mv (user_id, hour, guild_id);
create index listeners_hourly_mv_last_play on stats.listeners_hourly_mv (last_play);

-- The live side can land in an hour the rollup already has a row for, so the views sum the two
-- back into one row per key: a reader never has to know there are two sources.
create view stats.plays_hourly as
select x.guild_id, x.hour,
       sum(x.plays)::int as plays, sum(x.failed)::int as failed, sum(x.fakeouts)::int as fakeouts,
       sum(x.loops)::int as loops, sum(x.commands)::int as commands, sum(x.encores)::int as encores,
       sum(x.listeners)::int as listeners
from (
  select m.guild_id, m.hour, m.plays, m.failed, m.fakeouts, m.loops, m.commands, m.encores, m.listeners
  from stats.plays_hourly_mv m
  union all
  select p.guild_id,
         date_trunc('hour', p.at, 'UTC'),
         (p.ok)::int,
         (not p.ok and p.reason is distinct from 'fakeout')::int,
         (p.reason is not distinct from 'fakeout')::int,
         (p.ok and p.trigger = 'loop')::int,
         (p.ok and p.trigger = 'command')::int,
         (p.ok and p.trigger = 'encore')::int,
         case when p.ok then p.listeners else 0 end
  from stats.plays p
  where p.id > coalesce((select max(h.last_play) from stats.plays_hourly_mv h), 0)
) x
group by 1, 2;

create view stats.listeners_hourly as
select x.guild_id, x.user_id, x.hour,
       sum(x.heard)::int as heard, sum(x.fled)::int as fled, sum(x.triggered)::int as triggered
from (
  select m.guild_id, m.user_id, m.hour, m.heard, m.fled, m.triggered
  from stats.listeners_hourly_mv m
  union all
  select l.guild_id, l.user_id, date_trunc('hour', l.at, 'UTC'), l.ok::int, l.fled::int, 0
  from stats.listens l
  where l.play_id > coalesce((select max(h.last_play) from stats.listeners_hourly_mv h), 0)
  union all
  select p.guild_id, p.user_id, date_trunc('hour', p.at, 'UTC'), 0, 0, 1
  from stats.plays p
  where p.trigger = 'command' and p.ok and p.user_id is not null
    and p.id > coalesce((select max(h.last_play) from stats.listeners_hourly_mv h), 0)
) x
group by 1, 2, 3
having sum(x.heard) + sum(x.fled) + sum(x.triggered) > 0;

-- +goose Down
drop view stats.listeners_hourly;
drop view stats.plays_hourly;
drop materialized view stats.listeners_hourly_mv;
drop materialized view stats.plays_hourly_mv;
drop view stats.play_outcomes;
drop view stats.listens;
