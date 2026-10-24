-- +goose Up
-- A workflow's description, in its owner's words, searched with its name; and its place
-- among its owner's pinned workflows, from 1, empty when it is not pinned.
ALTER TABLE workflows ADD COLUMN description text NOT NULL DEFAULT '';
ALTER TABLE workflows ADD COLUMN pin integer;

CREATE INDEX workflows_pinned_idx ON workflows (owner_id, pin) WHERE pin IS NOT NULL;

-- +goose Down
DROP INDEX workflows_pinned_idx;
ALTER TABLE workflows DROP COLUMN pin;
ALTER TABLE workflows DROP COLUMN description;
