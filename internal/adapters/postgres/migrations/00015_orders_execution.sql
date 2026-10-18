-- +goose Up
-- An execution is recorded when it finishes, after the orders it places: their execution
-- is a reference to look it up by, not a key that must exist yet. Executions are also
-- pruned, while the books keep their orders.
ALTER TABLE orders DROP CONSTRAINT orders_execution_id_fkey;

-- +goose Down
UPDATE orders SET execution_id = NULL WHERE execution_id NOT IN (SELECT id FROM executions);
ALTER TABLE orders ADD CONSTRAINT orders_execution_id_fkey
    FOREIGN KEY (execution_id) REFERENCES executions (id) ON DELETE SET NULL;
