-- +goose Up
-- Which of the user's agents' and tokens' live actions go ahead without the user confirming
-- them (D406): none to start with.
ALTER TABLE users
    ADD COLUMN unconfirmed_go_live  boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_run      boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_change   boolean NOT NULL DEFAULT false,
    ADD COLUMN unconfirmed_kill_off boolean NOT NULL DEFAULT false;

-- A token's or an agent's live action waiting for its user, and how it ended (D406). Who
-- asked stays as it was, the grant or the token gone or not.
CREATE TABLE confirmations (
    id          uuid PRIMARY KEY,
    owner_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        text        NOT NULL CHECK (kind IN ('go_live', 'switch_on', 'run', 'change', 'kill_off')),
    workflow_id uuid REFERENCES workflows (id) ON DELETE CASCADE,
    grant_id    text        NOT NULL DEFAULT '',
    token_id    text        NOT NULL DEFAULT '',
    agent       text        NOT NULL DEFAULT '',
    scopes      text[]      NOT NULL,
    what        text        NOT NULL,
    payload     jsonb       NOT NULL,
    status      text        NOT NULL CHECK (status IN ('pending', 'confirmed', 'done', 'failed', 'refused', 'expired')),
    result      text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL,
    expires_at  timestamptz NOT NULL,
    decided_at  timestamptz
);

CREATE INDEX confirmations_owner_created_idx ON confirmations (owner_id, created_at DESC);

-- +goose Down
DROP TABLE confirmations;
ALTER TABLE users
    DROP COLUMN unconfirmed_go_live,
    DROP COLUMN unconfirmed_run,
    DROP COLUMN unconfirmed_change,
    DROP COLUMN unconfirmed_kill_off;
