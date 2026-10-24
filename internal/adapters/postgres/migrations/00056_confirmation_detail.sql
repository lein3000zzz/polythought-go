-- +goose Up
-- What a held action changes, line by line, for the user to read before they confirm it
-- (A3): a workflow's new graph against the one it has.
ALTER TABLE confirmations ADD COLUMN detail text NOT NULL DEFAULT '';

-- A change to a custom node that a workflow trading live and on uses waits too (A5).
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'custom_node', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps',
    'admin_code', 'admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin'
));

-- +goose Down
DELETE FROM confirmations WHERE kind = 'custom_node';
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps', 'admin_code',
    'admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin'
));
ALTER TABLE confirmations DROP COLUMN detail;
