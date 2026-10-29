-- +goose Up
-- Who switched each workflow and why (D433): on and off, paper or live and from which
-- wallet, paper resets and the kill switch; by the user, an agent or token, or Polythought.
CREATE TABLE workflow_switches (
    id             uuid        PRIMARY KEY,
    workflow_id    uuid        NOT NULL REFERENCES workflows (id) ON DELETE CASCADE,
    kind           text        NOT NULL CHECK (kind IN ('on', 'off', 'paper', 'live', 'wallet', 'reset', 'kill_on', 'kill_off')),
    by_grant       text        NOT NULL DEFAULT '',
    by_token       text        NOT NULL DEFAULT '',
    by_name        text        NOT NULL DEFAULT '',
    by_confirmed   boolean     NOT NULL DEFAULT false,
    polythought    boolean     NOT NULL DEFAULT false,
    reason         text        NOT NULL DEFAULT '',
    wallet_id      text        NOT NULL DEFAULT '',
    wallet_address text        NOT NULL DEFAULT '',
    at             timestamptz NOT NULL
);

CREATE INDEX workflow_switches_newest ON workflow_switches (workflow_id, id DESC);
CREATE INDEX workflow_switches_at ON workflow_switches (at);

-- A live action waiting for its user keeps why the agent asked, for when it is carried out.
ALTER TABLE confirmations ADD COLUMN reason text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE confirmations DROP COLUMN reason;
DROP TABLE workflow_switches;
