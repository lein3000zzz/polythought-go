-- +goose Up
-- Each workflow's highest and lowest profit and loss in a mode, each with when, as the
-- sampling found them (D430). since is what they count from: a paper account's start or its
-- last reset, a sample with a later one starting them over.
CREATE TABLE pnl_extremes (
    workflow_id uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    mode        text        NOT NULL CHECK (mode IN ('paper', 'live')),
    since       timestamptz NOT NULL,
    high        numeric     NOT NULL,
    high_at     timestamptz NOT NULL,
    low         numeric     NOT NULL,
    low_at      timestamptz NOT NULL,
    PRIMARY KEY (workflow_id, mode)
);

-- +goose Down
DROP TABLE pnl_extremes;
