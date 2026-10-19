-- +goose Up
-- The last setting up of each smart wallet's trading approvals through the Relayer
-- (D160, D161): what it grants, the batch its owner signs or signed, and how it went.
CREATE TABLE approval_setups (
    wallet_id      uuid PRIMARY KEY REFERENCES wallets (id) ON DELETE CASCADE,
    owner_address  text        NOT NULL, -- who signs for the wallet
    signature_type smallint    NOT NULL CHECK (signature_type BETWEEN 1 AND 3), -- proxy, Safe, Deposit Wallet
    approvals      jsonb       NOT NULL, -- [{token, spender, standard}]
    nonce          text        NOT NULL,
    deadline       timestamptz,          -- a Deposit Wallet's batch only
    state          text        NOT NULL CHECK (state IN ('signing', 'submitted', 'done', 'failed')),
    transaction_id text        NOT NULL DEFAULT '',
    error          text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL
);

CREATE INDEX approval_setups_submitted ON approval_setups (updated_at) WHERE state = 'submitted';

-- +goose Down
DROP TABLE approval_setups;
