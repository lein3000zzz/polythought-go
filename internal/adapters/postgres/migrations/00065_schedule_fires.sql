-- +goose Up
-- When each schedule that makes up missed runs last fired (D440): the run it was for, by
-- when it was due, so one started again after the server was down knows what it missed.
CREATE TABLE schedule_fires (
    workflow_id uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    node_id     text        NOT NULL,
    due         timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, node_id)
);

-- +goose Down
DROP TABLE schedule_fires;
