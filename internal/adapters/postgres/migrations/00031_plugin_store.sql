-- +goose Up
-- What plugin nodes keep (D288): a JSON value by key, per workflow node and plugin, up to
-- a size per node (D303). It counts as the node's memory, and goes as it does: with
-- Forget, with the node, and with the workflow.
CREATE TABLE plugin_store (
    workflow_id uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node_id     text        NOT NULL,
    plugin_id   text        NOT NULL,
    key         text        COLLATE "C" NOT NULL, -- in byte order, as plugins page them
    value       bytea       NOT NULL, -- JSON, exactly as the plugin gave it
    size        integer     NOT NULL CHECK (size > 0), -- bytes of the key and the value
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, node_id, plugin_id, key)
);

-- +goose Down
DROP TABLE plugin_store;
