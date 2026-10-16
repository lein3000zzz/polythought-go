-- +goose Up
-- Node types users define (D49): their settings, ports and items as the form has them,
-- and the TypeScript that runs them. Each owner's are named in a namespace of its own:
-- custom.<name> (D51).
CREATE TABLE custom_nodes (
    id          uuid PRIMARY KEY,
    owner_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    label       text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    settings    jsonb       NOT NULL, -- each setting as a node type describes one
    outputs     jsonb       NOT NULL, -- the output ports' names, in order
    items       jsonb       NOT NULL, -- passed on, or the fields of its own items
    source      text        NOT NULL,
    version     integer     NOT NULL CHECK (version > 0),
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    UNIQUE (owner_id, name)
);

-- +goose Down
DROP TABLE custom_nodes;
