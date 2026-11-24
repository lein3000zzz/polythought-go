-- +goose Up
-- Admin oversight (D435): an admin may switch off a user's workflow and hold it off until
-- an admin releases it; its history says an admin did.
ALTER TABLE workflows
    ADD COLUMN held_at     timestamptz,
    ADD COLUMN held_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN held_reason text NOT NULL DEFAULT '';

ALTER TABLE workflow_switches ADD COLUMN by_admin boolean NOT NULL DEFAULT false;
ALTER TABLE workflow_switches DROP CONSTRAINT workflow_switches_kind_check;
ALTER TABLE workflow_switches ADD CONSTRAINT workflow_switches_kind_check CHECK (kind IN (
    'on', 'off', 'hold', 'release', 'paper', 'live', 'wallet', 'reset', 'kill_on', 'kill_off'
));

-- An admin's agent's switching off of a user's workflow waits for the admin, as its work
-- on users does (D410).
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'custom_node', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps',
    'admin_code', 'admin_workflow', 'admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin'
));

-- +goose Down
DELETE FROM confirmations WHERE kind = 'admin_workflow';
ALTER TABLE confirmations DROP CONSTRAINT confirmations_kind_check;
ALTER TABLE confirmations ADD CONSTRAINT confirmations_kind_check CHECK (kind IN (
    'go_live', 'switch_on', 'run', 'change', 'custom_node', 'kill_off', 'admin_role', 'admin_disable', 'admin_caps',
    'admin_code', 'admin_config', 'admin_restart', 'admin_plugin_approve', 'admin_plugin'
));

DELETE FROM workflow_switches WHERE kind IN ('hold', 'release');
ALTER TABLE workflow_switches DROP CONSTRAINT workflow_switches_kind_check;
ALTER TABLE workflow_switches ADD CONSTRAINT workflow_switches_kind_check CHECK (kind IN (
    'on', 'off', 'paper', 'live', 'wallet', 'reset', 'kill_on', 'kill_off'
));
ALTER TABLE workflow_switches DROP COLUMN by_admin;

ALTER TABLE workflows DROP COLUMN held_reason, DROP COLUMN held_by, DROP COLUMN held_at;
