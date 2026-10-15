-- +goose Up
-- A wait that can be answered carries a random key, and an answer must bring it: buttons
-- on an old message cannot answer a later question. Waits without one cannot be answered.
ALTER TABLE waits ADD COLUMN key text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE waits DROP COLUMN key;
