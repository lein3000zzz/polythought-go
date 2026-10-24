-- +goose Up
-- An admin's agents' and tokens' work on the instance waits for the admin where they say so
-- (D411): changing Config, restarting, approving a plugin, its other changes; all of it to
-- start with.
ALTER TABLE users
    ADD COLUMN unconfirmed_config         boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_restart        boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_plugin_approve boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_plugin_change  boolean NOT NULL DEFAULT false;

ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps', 'admin_code',
    'admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin'
));

-- +goose Down
DELETE FROM confirmations WHERE kind IN ('admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin');
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps', 'admin_code'
));
ALTER TABLE users
    DROP COLUMN unconfirmed_config,
    DROP COLUMN unconfirmed_restart,
    DROP COLUMN unconfirmed_plugin_approve,
    DROP COLUMN unconfirmed_plugin_change;
