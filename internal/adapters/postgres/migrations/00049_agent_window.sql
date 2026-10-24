-- +goose Up
-- Whether a live action that waits for the user is also asked in the agent's own window,
-- where its client can ask (D409): off to start with.
ALTER TABLE users ADD COLUMN ask_in_agent boolean NOT NULL DEFAULT false;

-- Where a confirmation was decided: at a browser (app), on Telegram, or in the agent's
-- window (agent); empty while pending, or when it expired.
ALTER TABLE confirmations
    ADD COLUMN decided_via text NOT NULL DEFAULT '' CHECK (decided_via IN ('', 'app', 'telegram', 'agent'));

-- +goose Down
ALTER TABLE confirmations DROP COLUMN decided_via;
ALTER TABLE users DROP COLUMN ask_in_agent;
