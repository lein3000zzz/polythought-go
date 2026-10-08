-- +goose Up
CREATE TABLE users (
    id                uuid PRIMARY KEY,
    email             text        NOT NULL,
    email_normalized  text        NOT NULL UNIQUE,
    password_hash     text        NOT NULL,
    role              text        NOT NULL CHECK (role IN ('admin', 'user')),
    email_verified_at timestamptz,
    disabled_at       timestamptz,
    created_at        timestamptz NOT NULL,
    updated_at        timestamptz NOT NULL
);

-- Session and token secrets are stored only as SHA-256 hashes.
CREATE TABLE sessions (
    id                  uuid PRIMARY KEY,
    user_id             uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash          bytea       NOT NULL UNIQUE,
    user_agent          text        NOT NULL DEFAULT '',
    ip                  text        NOT NULL DEFAULT '',
    created_at          timestamptz NOT NULL,
    last_seen_at        timestamptz NOT NULL,
    expires_at          timestamptz NOT NULL, -- sliding idle expiry
    absolute_expires_at timestamptz NOT NULL
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    prefix       text        NOT NULL, -- first characters, to recognise a token in lists
    token_hash   bytea       NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz,
    last_used_at timestamptz
);

CREATE INDEX api_tokens_user_id_idx ON api_tokens (user_id);

CREATE TABLE invites (
    id         uuid PRIMARY KEY,
    code_hash  bytea       NOT NULL UNIQUE,
    created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_by    uuid        REFERENCES users (id) ON DELETE SET NULL,
    used_at    timestamptz
);

-- +goose Down
DROP TABLE invites;
DROP TABLE api_tokens;
DROP TABLE sessions;
DROP TABLE users;
