-- +goose Up
-- What a wait that can be answered asks, its text as sent and its options, for answering
-- it elsewhere than where it was asked (D405); empty for waits that ask nothing, and for
-- those made before.
ALTER TABLE waits ADD COLUMN question jsonb;

-- Whether the user lets their agents and API tokens answer their workflows' questions
-- (D405): off to start with, switched at a browser only.
ALTER TABLE users ADD COLUMN agents_may_answer boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN agents_may_answer;
ALTER TABLE waits DROP COLUMN question;
