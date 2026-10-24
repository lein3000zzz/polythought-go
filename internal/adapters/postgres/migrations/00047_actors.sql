-- +goose Up
-- The agent or API token behind a workflow's runs (D407), named as it was then: one that
-- switched it on, or changed it while on, until it is switched on or off again; and behind
-- each execution, one that ran it by hand too. Empty for the user.
ALTER TABLE workflows
    ADD COLUMN acted_how       text    NOT NULL DEFAULT '' CHECK (acted_how IN ('', 'switched_on', 'changed')),
    ADD COLUMN acted_grant     text    NOT NULL DEFAULT '',
    ADD COLUMN acted_token     text    NOT NULL DEFAULT '',
    ADD COLUMN acted_name      text    NOT NULL DEFAULT '',
    ADD COLUMN acted_confirmed boolean NOT NULL DEFAULT false;

ALTER TABLE executions
    ADD COLUMN by_how       text    NOT NULL DEFAULT '' CHECK (by_how IN ('', 'run', 'switched_on', 'changed')),
    ADD COLUMN by_grant     text    NOT NULL DEFAULT '',
    ADD COLUMN by_token     text    NOT NULL DEFAULT '',
    ADD COLUMN by_name      text    NOT NULL DEFAULT '',
    ADD COLUMN by_confirmed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE executions
    DROP COLUMN by_how, DROP COLUMN by_grant, DROP COLUMN by_token, DROP COLUMN by_name, DROP COLUMN by_confirmed;
ALTER TABLE workflows
    DROP COLUMN acted_how, DROP COLUMN acted_grant, DROP COLUMN acted_token, DROP COLUMN acted_name, DROP COLUMN acted_confirmed;
