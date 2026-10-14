-- +goose Up
-- Settings an admin saved on the Config page. A setting without a row uses its default,
-- and an environment variable overrides both. A secret is sealed by the application's
-- SecretBox, bound to its key, and kept in sealed instead of value.
CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb,
    sealed     bytea,
    updated_at timestamptz NOT NULL,
    updated_by uuid REFERENCES users (id) ON DELETE SET NULL,
    CHECK ((value IS NULL) <> (sealed IS NULL))
);

-- Every change to a setting: the value that applied before and after. Secret values are
-- never recorded, only that one changed.
CREATE TABLE settings_audit (
    id         uuid PRIMARY KEY, -- UUIDv7, so IDs sort by time
    key        text        NOT NULL,
    old_value  jsonb,
    new_value  jsonb,
    secret     boolean     NOT NULL,
    actor_id   uuid REFERENCES users (id) ON DELETE SET NULL,
    changed_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE settings_audit;
DROP TABLE settings;
