-- +goose Up
-- The share of loop visits, in percent, that sit out the suspense and leave without playing. Capped
-- at 50 so a fake-out stays the exception; 0 is the behaviour every guild had before this.
alter table guilds.settings
  add column fakeout smallint not null default 0 check (fakeout between 0 and 50);

-- +goose Down
alter table guilds.settings drop column fakeout;
