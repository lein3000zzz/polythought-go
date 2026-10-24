-- +goose Up
-- The name of the agent or API token a task is handed to, as it was then (D413), for the
-- app to say who has it.
ALTER TABLE waits ADD COLUMN lease_name text;

-- +goose Down
ALTER TABLE waits DROP COLUMN lease_name;
