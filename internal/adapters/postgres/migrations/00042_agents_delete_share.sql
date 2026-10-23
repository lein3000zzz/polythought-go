-- +goose Up
-- Whether the user lets their agents and API tokens delete workflows and share them to the
-- library (D403): off to start with, switched at a browser only.
ALTER TABLE users
    ADD COLUMN agents_may_delete boolean NOT NULL DEFAULT false,
    ADD COLUMN agents_may_share  boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
    DROP COLUMN agents_may_delete,
    DROP COLUMN agents_may_share;
