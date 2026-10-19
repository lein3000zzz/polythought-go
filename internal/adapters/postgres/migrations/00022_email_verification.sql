-- +goose Up
-- Accounts from before email verification count as verified (D167); from now on, an
-- account without email_verified_at must verify before it signs in (D166).
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

CREATE INDEX users_unverified ON users (created_at) WHERE email_verified_at IS NULL;

-- Links mailed to an account: to verify its address, or to set a new password. Only their
-- SHA-256 hashes are kept, and each works once, until it expires.
CREATE TABLE email_tokens (
    token_hash bytea PRIMARY KEY,
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('verify', 'reset')),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX email_tokens_user ON email_tokens (user_id, purpose, created_at);

-- +goose Down
-- Who was verified before stays so.
DROP TABLE email_tokens;
DROP INDEX users_unverified;
