-- +goose Up
-- An admin's caps on a user's agent spend (D271): NULL is the instance's, 0 none; dollars
-- to the cent, as providers report them. And whether the user's kill switch stops their
-- agents too, their choice (D276).
ALTER TABLE users
    ADD COLUMN max_agent_dollars numeric CHECK (max_agent_dollars >= 0),
    ADD COLUMN max_agent_input   bigint  CHECK (max_agent_input >= 0),
    ADD COLUMN max_agent_output  bigint  CHECK (max_agent_output >= 0),
    ADD COLUMN max_agent_total   bigint  CHECK (max_agent_total >= 0),
    ADD COLUMN kill_stops_agents boolean NOT NULL DEFAULT false;

-- The caps the month's 80% and cap notices went out at, so each goes once, and again
-- only for a cap raised since (D274).
ALTER TABLE agent_usage
    ADD COLUMN warned_at   timestamptz,
    ADD COLUMN warned_caps text,
    ADD COLUMN capped_at   timestamptz,
    ADD COLUMN capped_caps text;

-- +goose Down
ALTER TABLE agent_usage
    DROP COLUMN warned_at,
    DROP COLUMN warned_caps,
    DROP COLUMN capped_at,
    DROP COLUMN capped_caps;

ALTER TABLE users
    DROP COLUMN max_agent_dollars,
    DROP COLUMN max_agent_input,
    DROP COLUMN max_agent_output,
    DROP COLUMN max_agent_total,
    DROP COLUMN kill_stops_agents;
