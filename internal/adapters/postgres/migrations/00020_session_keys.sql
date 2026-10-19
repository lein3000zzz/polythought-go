-- +goose Up
-- Deposit Wallets' session keys (D156): made by the server, authorised by the wallet's
-- owner in the browser, and submitted to Polymarket's Relayer. A key is sealed as
-- credentials are until it is authorised; then it moves to its wallet's credentials,
-- which it made, and the row keeps only its address. At most one active key a wallet:
-- a new one replaces it once confirmed.
CREATE TABLE session_keys (
    wallet_id      uuid        NOT NULL REFERENCES wallets (id) ON DELETE CASCADE,
    signer_address text        NOT NULL, -- the key's address, EIP-55
    owner_address  text        NOT NULL, -- who controls the Deposit Wallet and signs for it
    key_sealed     bytea,
    state          text        NOT NULL CHECK (state IN ('signing', 'authorizing', 'active', 'failed')),
    valid_until    timestamptz NOT NULL,
    nonce          text        NOT NULL, -- of the batch signed last, or to sign
    deadline       timestamptz NOT NULL,
    transaction_id text        NOT NULL DEFAULT '',
    error          text        NOT NULL DEFAULT '',
    reminded_at    timestamptz,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    PRIMARY KEY (wallet_id, signer_address),
    CHECK ((state = 'active') = (key_sealed IS NULL))
);

CREATE UNIQUE INDEX session_keys_one_active ON session_keys (wallet_id) WHERE state = 'active';
CREATE INDEX session_keys_authorizing ON session_keys (updated_at) WHERE state = 'authorizing';
CREATE INDEX session_keys_expiring ON session_keys (valid_until) WHERE state = 'active' AND reminded_at IS NULL;

-- Credentials a session key made: it signs orders in the session envelope.
ALTER TABLE polymarket_credentials ADD COLUMN session_key boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE polymarket_credentials DROP COLUMN session_key;
DROP TABLE session_keys;
