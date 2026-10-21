-- +goose Up
-- What a user's agent nodes spent with their own keys, by UTC calendar month (D268): the
-- tokens every call took, and the dollars providers reported, which only some do (D270).
-- No row is nothing spent. A user's rows go with the user.
CREATE TABLE agent_usage (
    user_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    month         timestamptz NOT NULL, -- 00:00 UTC on its first day
    input_tokens  bigint      NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint      NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    dollars       numeric     NOT NULL DEFAULT 0 CHECK (dollars >= 0),
    calls         integer     NOT NULL DEFAULT 0 CHECK (calls >= 0),
    costed_calls  integer     NOT NULL DEFAULT 0 CHECK (costed_calls >= 0), -- those a provider reported dollars for
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (user_id, month)
);

-- +goose Down
DROP TABLE agent_usage;
