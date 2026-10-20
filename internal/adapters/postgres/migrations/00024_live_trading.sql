-- +goose Up
-- How a workflow trades (D191): on paper unless it says live, from one of its owner's
-- wallets. A workflow without a row trades on paper (D22).
CREATE TABLE workflow_trading (
    workflow_id uuid PRIMARY KEY REFERENCES workflows (id) ON DELETE CASCADE,
    mode        text        NOT NULL CHECK (mode IN ('paper', 'live')),
    wallet_id   uuid REFERENCES wallets (id) ON DELETE SET NULL,
    updated_at  timestamptz NOT NULL
);

CREATE INDEX workflow_trading_by_wallet ON workflow_trading (wallet_id) WHERE wallet_id IS NOT NULL;

-- Live orders and holdings are the record of real money: they outlive their workflow,
-- named by it (D199). Paper ones go with it, by the trigger below.
ALTER TABLE orders DROP CONSTRAINT orders_workflow_id_fkey;
ALTER TABLE holdings DROP CONSTRAINT holdings_workflow_id_fkey;

ALTER TABLE orders
    ADD COLUMN workflow_name text NOT NULL DEFAULT '', -- once the workflow is deleted
    ADD COLUMN wallet_id     uuid REFERENCES wallets (id) ON DELETE SET NULL, -- a live order's
    ADD COLUMN clob_id       text NOT NULL DEFAULT ''; -- a live order's ID on the CLOB: its hash

CREATE UNIQUE INDEX orders_by_clob_id ON orders (clob_id) WHERE clob_id <> '';

-- A live order sent and not settled yet, and one whose trades failed (D193, D204).
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('filled', 'partial', 'unfilled', 'refused', 'pending', 'failed'));

CREATE INDEX orders_pending ON orders (created_at) WHERE status = 'pending';

-- A live fill's trade, and how far it has come on chain; '' for paper.
ALTER TABLE order_fills
    ADD COLUMN trade_id text NOT NULL DEFAULT '',
    ADD COLUMN status   text NOT NULL DEFAULT ''
        CHECK (status IN ('', 'matched', 'mined', 'retrying', 'confirmed', 'failed'));

CREATE INDEX order_fills_following ON order_fills (order_id) WHERE status IN ('matched', 'mined', 'retrying');

-- A holding's owner, for those whose workflow is gone.
ALTER TABLE holdings
    ADD COLUMN owner_id      uuid REFERENCES users (id) ON DELETE CASCADE,
    ADD COLUMN workflow_name text NOT NULL DEFAULT '';

UPDATE holdings h SET owner_id = w.owner_id FROM workflows w WHERE w.id = h.workflow_id;
ALTER TABLE holdings ALTER COLUMN owner_id SET NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION workflow_books_on_delete() RETURNS trigger AS $$
BEGIN
    DELETE FROM orders WHERE workflow_id = OLD.id AND mode = 'paper';
    DELETE FROM holdings WHERE workflow_id = OLD.id AND mode = 'paper';
    UPDATE orders SET workflow_name = OLD.name WHERE workflow_id = OLD.id;
    UPDATE holdings SET workflow_name = OLD.name WHERE workflow_id = OLD.id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER workflow_books_on_delete
    BEFORE DELETE ON workflows
    FOR EACH ROW EXECUTE FUNCTION workflow_books_on_delete();

-- +goose Down
DROP TRIGGER workflow_books_on_delete ON workflows;
DROP FUNCTION workflow_books_on_delete();

DELETE FROM orders WHERE workflow_id NOT IN (SELECT id FROM workflows);
DELETE FROM holdings WHERE workflow_id NOT IN (SELECT id FROM workflows);
DELETE FROM orders WHERE status IN ('pending', 'failed');

ALTER TABLE holdings DROP COLUMN workflow_name, DROP COLUMN owner_id;

DROP INDEX order_fills_following;
ALTER TABLE order_fills DROP COLUMN status, DROP COLUMN trade_id;

DROP INDEX orders_pending;
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('filled', 'partial', 'unfilled', 'refused'));

DROP INDEX orders_by_clob_id;
ALTER TABLE orders DROP COLUMN clob_id, DROP COLUMN wallet_id, DROP COLUMN workflow_name;

ALTER TABLE holdings ADD CONSTRAINT holdings_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows (id) ON DELETE CASCADE;
ALTER TABLE orders ADD CONSTRAINT orders_workflow_id_fkey
    FOREIGN KEY (workflow_id) REFERENCES workflows (id) ON DELETE CASCADE;

DROP TABLE workflow_trading;
