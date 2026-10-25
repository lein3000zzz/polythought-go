-- +goose Up
-- The keys a user set for the workflow editor's shortcuts, by action, where they changed
-- them in Settings (D420); the rest keep theirs.
ALTER TABLE users ADD COLUMN shortcuts jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE users DROP COLUMN shortcuts;
