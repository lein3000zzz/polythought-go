-- +goose Up
-- A plugin's own settings, as its manifest declares them (D329), set on the Plugins page:
-- a setting without a row is at its default. A secret is sealed by the application's
-- SecretBox, bound to its plugin and key, and kept in sealed instead of value. Changes
-- are audited in plugin_audit.
CREATE TABLE plugin_settings (
    plugin_id  text        NOT NULL REFERENCES plugins (id) ON DELETE CASCADE,
    key        text        NOT NULL,
    value      jsonb,
    sealed     bytea,
    updated_at timestamptz NOT NULL,
    updated_by uuid REFERENCES users (id) ON DELETE SET NULL,
    PRIMARY KEY (plugin_id, key),
    CHECK ((value IS NULL) <> (sealed IS NULL))
);

-- +goose Down
DROP TABLE plugin_settings;
