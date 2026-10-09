-- +goose Up
-- Workflows members shared for others to start from. Built-in scenarios live in code.
CREATE TABLE templates (
    id                 uuid PRIMARY KEY,
    author_id          uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Sharing the same workflow again replaces its copy; the copy outlives the workflow.
    source_workflow_id uuid        REFERENCES workflows (id) ON DELETE SET NULL,
    name               text        NOT NULL,
    summary            text        NOT NULL DEFAULT '',
    description        text        NOT NULL DEFAULT '',
    definition         jsonb       NOT NULL, -- nodes and connections, as in workflows
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    UNIQUE (author_id, source_workflow_id)
);

CREATE INDEX templates_author_id_idx ON templates (author_id);

-- +goose Down
DROP TABLE templates;
