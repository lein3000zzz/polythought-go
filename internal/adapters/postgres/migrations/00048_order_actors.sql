-- +goose Up
-- The agent or API token behind an order (D408), as its run had it when it was placed,
-- kept with the order, which outlives the execution: empty for the user.
ALTER TABLE orders
    ADD COLUMN by_how       text    NOT NULL DEFAULT '' CHECK (by_how IN ('', 'run', 'switched_on', 'changed')),
    ADD COLUMN by_grant     text    NOT NULL DEFAULT '',
    ADD COLUMN by_token     text    NOT NULL DEFAULT '',
    ADD COLUMN by_name      text    NOT NULL DEFAULT '',
    ADD COLUMN by_confirmed boolean NOT NULL DEFAULT false;

CREATE INDEX orders_owner_by_idx ON orders (owner_id, by_grant, by_token) WHERE by_grant <> '' OR by_token <> '';

-- +goose Down
DROP INDEX orders_owner_by_idx;
ALTER TABLE orders
    DROP COLUMN by_how, DROP COLUMN by_grant, DROP COLUMN by_token, DROP COLUMN by_name, DROP COLUMN by_confirmed;
