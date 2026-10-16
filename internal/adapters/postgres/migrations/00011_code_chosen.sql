-- +goose Up
-- The users an admin picked to write code, for when code access is "chosen" (D45).
ALTER TABLE users ADD COLUMN code_chosen boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN code_chosen;
