-- +goose Up
-- Each user's vault: typed credentials, such as API keys, that nodes use on their behalf.
-- Values that are not secret are kept in the clear, to show; the secret ones are sealed
-- together by the application's SecretBox and bound to the credential's ID.
CREATE TABLE credentials (
    id         uuid PRIMARY KEY,
    owner_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type       text        NOT NULL, -- a credential type registered in code, e.g. http.header
    name       text        NOT NULL,
    fields     jsonb       NOT NULL, -- the values that are not secret, by field name
    secrets    jsonb       NOT NULL, -- the names of the secret values that are set
    sealed     bytea,                -- the secret values as JSON, sealed; null when none are set
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    tested_at  timestamptz,
    test_error text        NOT NULL DEFAULT '', -- why the last test failed; empty when it passed
    UNIQUE (owner_id, name)
);

-- +goose Down
DROP TABLE credentials;
