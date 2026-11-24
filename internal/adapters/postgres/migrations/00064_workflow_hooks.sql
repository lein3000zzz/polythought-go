-- +goose Up
-- Inbound webhooks (D439): each Inbound webhook trigger's secret address, /hooks/<secret>,
-- found by the secret's hash; the secret itself sealed, for its owner to copy again.
-- Renewing it replaces the secret in place.
CREATE TABLE workflow_hooks (
    workflow_id uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node_id     text        NOT NULL,
    secret_hash bytea       NOT NULL UNIQUE,
    sealed      bytea       NOT NULL,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, node_id)
);

-- +goose Down
DROP TABLE workflow_hooks;
