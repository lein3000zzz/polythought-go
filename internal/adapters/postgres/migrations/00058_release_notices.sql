-- +goose Up
-- The releases admins were told of (D424): each once, whatever restarts in between.
CREATE TABLE release_notices (
    version text PRIMARY KEY,
    told_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE release_notices;
