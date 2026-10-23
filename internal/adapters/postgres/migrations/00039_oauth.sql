-- +goose Up
-- Agents sign in with OAuth 2.1 (D382, D397): clients, a user's grant to each, and the codes
-- and tokens of a grant, kept as their hashes.

-- A client is registered by the agent (dynamic registration) or is a metadata document at
-- an https URL, its ID, fetched and kept a while.
CREATE TABLE oauth_clients (
    id            text PRIMARY KEY,
    kind          text        NOT NULL CHECK (kind IN ('registered', 'document')),
    name          text        NOT NULL,
    redirect_uris text[]      NOT NULL,
    client_uri    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL,
    fetched_at    timestamptz
);

-- One grant for each user and client, its scopes what the user allowed last.
CREATE TABLE oauth_grants (
    id           uuid PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id    text        NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    scopes       text[]      NOT NULL,
    resource     text        NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    UNIQUE (user_id, client_id)
);

-- A code works once, for minutes; used_at says it was, so a second use is caught.
CREATE TABLE oauth_codes (
    hash         bytea PRIMARY KEY,
    grant_id     uuid        NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    redirect_uri text        NOT NULL,
    challenge    text        NOT NULL,
    scopes       text[]      NOT NULL,
    resource     text        NOT NULL,
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz
);

-- Access tokens and refresh tokens; a refresh token works once, used_at set as it is
-- rotated, so one used again shows it was copied.
CREATE TABLE oauth_tokens (
    hash       bytea PRIMARY KEY,
    grant_id   uuid        NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('access', 'refresh')),
    scopes     text[]      NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE INDEX oauth_tokens_grant ON oauth_tokens (grant_id);
CREATE INDEX oauth_tokens_expires ON oauth_tokens (expires_at);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
