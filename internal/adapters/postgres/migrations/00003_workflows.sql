-- +goose Up
CREATE TABLE workflows (
    id         uuid PRIMARY KEY,
    owner_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    active     boolean     NOT NULL DEFAULT false,
    definition jsonb       NOT NULL, -- nodes and connections
    version    integer     NOT NULL CHECK (version > 0),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX workflows_owner_id_idx ON workflows (owner_id, id);
CREATE INDEX workflows_active_idx ON workflows (id) WHERE active;

CREATE TABLE executions (
    id               uuid PRIMARY KEY,
    workflow_id      uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    workflow_version integer     NOT NULL,
    triggered_by     text        NOT NULL,
    status           text        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'cancelled')),
    started_at       timestamptz NOT NULL,
    finished_at      timestamptz,
    error            text        NOT NULL DEFAULT '',
    runs             jsonb       NOT NULL -- per-node status, timings and item counts
);

CREATE INDEX executions_workflow_id_idx ON executions (workflow_id, id DESC);
CREATE INDEX executions_finished_at_idx ON executions (finished_at);

-- +goose Down
DROP TABLE executions;
DROP TABLE workflows;
