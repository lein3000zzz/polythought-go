-- +goose Up
-- Orders that rest on the book (3p3): open until filled, cancelled or expired, keeping
-- what they filled.
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('filled', 'partial', 'unfilled', 'refused', 'pending', 'failed', 'open', 'canceled', 'expired'));

CREATE INDEX orders_open ON orders (mode, workflow_id, id) WHERE status = 'open';

-- +goose Down
DROP INDEX orders_open;
UPDATE orders SET status = 'partial' WHERE status IN ('canceled', 'expired') AND EXISTS (
    SELECT 1 FROM order_fills f WHERE f.order_id = orders.id);
UPDATE orders SET status = 'unfilled' WHERE status IN ('open', 'canceled', 'expired');
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check
    CHECK (status IN ('filled', 'partial', 'unfilled', 'refused', 'pending', 'failed'));
