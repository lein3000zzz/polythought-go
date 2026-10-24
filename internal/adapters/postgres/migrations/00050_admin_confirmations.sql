-- +goose Up
-- An admin's agents' and tokens' work on other users waits for the admin where they say so
-- (D410): role changes, disabling, raising caps, code access; all of it to start with.
ALTER TABLE users
    ADD COLUMN unconfirmed_role    boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_disable boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_caps    boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_code    boolean NOT NULL DEFAULT false;

-- Whom a confirmation is about, such as a user's email: the admin's to see, not the agent's.
ALTER TABLE confirmations ADD COLUMN about text NOT NULL DEFAULT '';

ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps', 'admin_code'
));

-- +goose Down
DELETE FROM confirmations WHERE kind LIKE 'admin_%';
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN ('go_live', 'switch_on', 'run', 'change', 'kill_off'));
ALTER TABLE confirmations DROP COLUMN about;
ALTER TABLE users
    DROP COLUMN unconfirmed_role,
    DROP COLUMN unconfirmed_disable,
    DROP COLUMN unconfirmed_caps,
    DROP COLUMN unconfirmed_code;
