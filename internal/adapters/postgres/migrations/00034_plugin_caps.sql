-- +goose Up
-- Each plugin's own caps, set on its card (D346): MiB of its data, its directory and its
-- temporary one together (D341), and of its process's memory (D343); NULL for the Config
-- page's.
ALTER TABLE plugins
    ADD COLUMN data_cap   integer CHECK (data_cap > 0),
    ADD COLUMN memory_cap integer CHECK (memory_cap > 0);

-- What admins remove of a plugin no longer installed: its nodes' stores, by plugin.
CREATE INDEX plugin_store_plugin ON plugin_store (plugin_id);

-- +goose Down
DROP INDEX plugin_store_plugin;
ALTER TABLE plugins DROP COLUMN memory_cap, DROP COLUMN data_cap;
