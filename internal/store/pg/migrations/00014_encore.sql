-- +goose Up
-- The share of loop visits, in percent, after which the bot comes back 10-30s later for a second
-- sound. Capped at 50 so an encore stays the exception; 0 is the behaviour every guild had before this.
alter table guilds.settings
  add column encore smallint not null default 0 check (encore between 0 and 50);

-- An encore is recorded under its own trigger, so it never reads as a loop visit of its own.
alter table stats.plays
  drop constraint plays_trigger_check,
  add constraint plays_trigger_check check (trigger in ('loop','command','encore'));

-- +goose Down
delete from stats.plays where trigger = 'encore';
alter table stats.plays
  drop constraint plays_trigger_check,
  add constraint plays_trigger_check check (trigger in ('loop','command'));
alter table guilds.settings drop column encore;
