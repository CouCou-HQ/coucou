-- +goose Up
-- A timed opt-out. Null keeps the original meaning — until the user says otherwise — so every row
-- written before this migration is already correct. Read once at boot and compared in memory after
-- that, so there is no index to earn here.
alter table optouts add column until timestamptz;

-- +goose Down
alter table optouts drop column until;
