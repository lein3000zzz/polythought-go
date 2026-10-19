-- +goose Up
-- The address an invite was mailed to (D180): only it signs up with the invite (D181).
-- Empty for an invite whose link an admin copied.
ALTER TABLE invites ADD COLUMN email text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE invites DROP COLUMN email;
