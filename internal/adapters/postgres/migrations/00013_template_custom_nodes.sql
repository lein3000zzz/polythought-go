-- +goose Up
-- A shared workflow brings the custom nodes it uses, as their author had them (A3).
ALTER TABLE templates ADD COLUMN custom_nodes jsonb NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE templates DROP COLUMN custom_nodes;
