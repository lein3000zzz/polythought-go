-- +goose Up
-- The parts of the workflows' dashboard a user hid (D429), where they chose on it; the
-- rest show.
ALTER TABLE users ADD COLUMN dashboard jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose Down
ALTER TABLE users DROP COLUMN dashboard;
