-- +goose Up
-- The task a local agent node hands its owner's agents (D386): its prompt, its items and
-- the shape an answer keeps to; empty for waits that hand none.
ALTER TABLE waits ADD COLUMN task jsonb;

-- The agent a task is handed to, its grant or API token, and until when (D412): one that
-- neither answers nor waits again by then hands it back.
ALTER TABLE waits ADD COLUMN lease_holder text;
ALTER TABLE waits ADD COLUMN lease_until timestamptz;

CREATE INDEX waits_tasks_idx ON waits (owner_id, created_at) WHERE task IS NOT NULL;

-- +goose Down
DROP INDEX waits_tasks_idx;
ALTER TABLE waits DROP COLUMN lease_until;
ALTER TABLE waits DROP COLUMN lease_holder;
ALTER TABLE waits DROP COLUMN task;
