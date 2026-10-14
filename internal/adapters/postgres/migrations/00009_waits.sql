-- +goose Up
-- Executions can wait: paused at a node until a time or an answer, and carried on later.
ALTER TABLE executions DROP CONSTRAINT executions_status_check;
ALTER TABLE executions ADD CONSTRAINT executions_status_check
    CHECK (status IN ('running', 'waiting', 'succeeded', 'failed', 'cancelled'));
ALTER TABLE executions ADD COLUMN waiting_on text NOT NULL DEFAULT '';
ALTER TABLE executions ADD COLUMN waiting_until timestamptz;

-- One row per waiting execution: the engine's snapshot of it, until it resumes. Taking a
-- row resumes it, so it resumes once.
CREATE TABLE waits (
    execution_id uuid PRIMARY KEY REFERENCES executions (id) ON DELETE CASCADE,
    workflow_id  uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    owner_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    node_id      text        NOT NULL,
    until        timestamptz NOT NULL, -- resumed then at the latest
    state        jsonb       NOT NULL,
    created_at   timestamptz NOT NULL
);

CREATE INDEX waits_until_idx ON waits (until);
CREATE INDEX waits_owner_id_idx ON waits (owner_id);
CREATE INDEX waits_workflow_id_idx ON waits (workflow_id);

-- +goose Down
DROP TABLE waits;
DELETE FROM executions WHERE status = 'waiting';
ALTER TABLE executions DROP COLUMN waiting_until;
ALTER TABLE executions DROP COLUMN waiting_on;
ALTER TABLE executions DROP CONSTRAINT executions_status_check;
ALTER TABLE executions ADD CONSTRAINT executions_status_check
    CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled'));
