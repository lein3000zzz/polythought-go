-- +goose Up
-- The key each plugin's ID is bound to (D365): the one a trusted key verified on its first
-- signed version, which its later versions must be signed by. Apart from the plugin's row,
-- as it stays while the plugin's data is left behind (D366).
CREATE TABLE plugin_keys (
    plugin_id text        PRIMARY KEY,
    key_id    text        NOT NULL,
    signer    text        NOT NULL, -- the trusted key's name, as it bound the ID
    bound_at  timestamptz NOT NULL
);

-- +goose Down
DROP TABLE plugin_keys;
