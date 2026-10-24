-- +goose Up
-- Whether the user lets their agents and API tokens save and run workflows that use
-- credentials, test credentials and change where they go (D404): off to start with,
-- switched at a browser only.
ALTER TABLE users
    ADD COLUMN agents_use_credentials boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
    DROP COLUMN agents_use_credentials;
