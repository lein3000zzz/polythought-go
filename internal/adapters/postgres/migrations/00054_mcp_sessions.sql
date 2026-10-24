-- +goose Up
-- The agents' and API tokens' sessions on /mcp (D414), kept so a restart does not forget
-- who is connected: a row while its session is open, from the server's run it opened in
-- (boot); one from a run before stays until its agent opens another, or it goes idle.
CREATE TABLE mcp_sessions (
    id        text PRIMARY KEY,
    user_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    holder    text        NOT NULL, -- grant:<id> or token:<id>
    name      text        NOT NULL, -- the agent or token, by its name then
    client    text        NOT NULL, -- its client, as it said, with its version
    boot      text        NOT NULL,
    opened_at timestamptz NOT NULL,
    seen_at   timestamptz NOT NULL
);

CREATE INDEX mcp_sessions_user_id_idx ON mcp_sessions (user_id);

-- +goose Down
DROP TABLE mcp_sessions;
