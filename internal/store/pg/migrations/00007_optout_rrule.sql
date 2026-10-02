-- +goose Up
-- A recurring opt-out, as an RFC 5545 rule. The string carries its own DTSTART and TZID, so the
-- zone needs no column of its own; window_s is the length of each occurrence, which RRULE has no
-- way to express — a rule yields instants, and an opt-out is an interval.
--
-- A row carries either until or rrule, never both: one is a deadline, the other a schedule.
alter table optouts
  add column rrule    text,
  add column window_s integer check (window_s is null or window_s > 0);

-- +goose Down
alter table optouts drop column rrule, drop column window_s;
