-- +goose Up
-- What nodes remember across runs and restarts (D121): per workflow node, keys with when
-- they were remembered, each until it expires. Kept while the node is in its workflow.
CREATE TABLE node_memory (
    workflow_id uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node_id     text        NOT NULL,
    key         text        NOT NULL,
    at          timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, node_id, key)
);

-- The oldest go first past a node's limit; expired ones are swept.
CREATE INDEX node_memory_at ON node_memory (workflow_id, node_id, at);
CREATE INDEX node_memory_expires_at ON node_memory (expires_at);

-- +goose Down
DROP TABLE node_memory;
