-- +goose Up
-- Requests to join (D380, D394): someone asks for an invite with their address and a note,
-- confirms the address by a mailed link, and an admin approves, mailing them an invite
-- (invite_id), or denies.
CREATE TABLE join_requests (
    id               uuid PRIMARY KEY,
    email            text        NOT NULL,
    email_normalized text        NOT NULL,
    note             text        NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    -- The confirmation link's, until it is opened; mails counts the links sent.
    token_hash       bytea UNIQUE,
    link_expires_at  timestamptz NOT NULL,
    mails            integer     NOT NULL DEFAULT 1,
    mailed_at        timestamptz NOT NULL,
    created_at       timestamptz NOT NULL,
    confirmed_at     timestamptz,
    decided_at       timestamptz,
    decided_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    approved         boolean,
    invite_id        uuid REFERENCES invites (id) ON DELETE SET NULL
);

-- An address has one open request at most.
CREATE UNIQUE INDEX join_requests_open ON join_requests (email_normalized) WHERE decided_at IS NULL;
CREATE INDEX join_requests_decided ON join_requests (email_normalized, decided_at);

-- +goose Down
DROP TABLE join_requests;
