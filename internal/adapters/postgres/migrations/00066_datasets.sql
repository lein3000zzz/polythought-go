-- +goose Up
-- Datasets: each user's named tables of JSON rows, which notebooks publish and workflows
-- read, or workflows record and notebooks read. A replace is staged, then swapped in at
-- once: rows staged under a stage of their own become the dataset's in one transaction.
ALTER TABLE users ADD COLUMN data_chosen boolean NOT NULL DEFAULT false;

CREATE TABLE datasets (
    id         uuid        PRIMARY KEY,
    owner_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name       text        NOT NULL,
    key_field  text        NOT NULL DEFAULT '',
    keep_days  integer     NOT NULL DEFAULT 0,
    keep_rows  integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    UNIQUE (owner_id, name)
);

CREATE TABLE dataset_stages (
    id         uuid        PRIMARY KEY,
    dataset_id uuid        NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL
);

-- A row belongs to its dataset, or to a stage of it until that is swapped in.
CREATE TABLE dataset_rows (
    dataset_id uuid        NOT NULL REFERENCES datasets (id) ON DELETE CASCADE,
    seq        bigserial,
    stage_id   uuid        REFERENCES dataset_stages (id) ON DELETE CASCADE,
    key        text        NOT NULL DEFAULT '',
    at         timestamptz NOT NULL,
    row        jsonb       NOT NULL,
    size       integer     NOT NULL,
    PRIMARY KEY (dataset_id, seq)
);

CREATE INDEX dataset_rows_key_idx ON dataset_rows (dataset_id, key) WHERE stage_id IS NULL;
CREATE INDEX dataset_rows_at_idx ON dataset_rows (dataset_id, at) WHERE stage_id IS NULL;
CREATE INDEX dataset_rows_stage_idx ON dataset_rows (stage_id) WHERE stage_id IS NOT NULL;

-- +goose Down
DROP TABLE dataset_rows;
DROP TABLE dataset_stages;
DROP TABLE datasets;
ALTER TABLE users DROP COLUMN data_chosen;
