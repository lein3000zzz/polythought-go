-- +goose Up
-- A wallet's private key, when its owner imported one (D21, D153), sealed as its API
-- credentials are, which are made from it (D154). Pasted credentials have none.
ALTER TABLE polymarket_credentials ADD COLUMN private_key_sealed bytea;

-- +goose Down
ALTER TABLE polymarket_credentials DROP COLUMN private_key_sealed;
