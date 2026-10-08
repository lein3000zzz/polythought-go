-- +goose Up
CREATE TABLE wallets (
    id                 uuid PRIMARY KEY,
    user_id            uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    address            text        NOT NULL, -- EIP-55 checksummed
    address_normalized text        NOT NULL, -- lowercase, for uniqueness
    label              text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL,
    UNIQUE (user_id, address_normalized)
);

-- Polymarket L2 API credentials. The three secrets are sealed by the application's
-- SecretBox (envelope encryption) and bound to their wallet and field.
CREATE TABLE polymarket_credentials (
    wallet_id         uuid PRIMARY KEY REFERENCES wallets (id) ON DELETE CASCADE,
    api_key_sealed    bytea       NOT NULL,
    secret_sealed     bytea       NOT NULL,
    passphrase_sealed bytea       NOT NULL,
    api_key_hint      text        NOT NULL, -- a few characters, to recognise the key
    signer_address    text        NOT NULL, -- POLY_ADDRESS; differs from the wallet for proxy and safe accounts
    signature_type    smallint    NOT NULL CHECK (signature_type BETWEEN 0 AND 3),
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL
);

-- +goose Down
DROP TABLE polymarket_credentials;
DROP TABLE wallets;
