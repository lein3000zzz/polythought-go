-- +goose Up
-- When a paper account was last started over (D107): its orders before stay, under it.
ALTER TABLE paper_accounts ADD COLUMN reset_at timestamptz;

-- A holding closed by its market's resolution (D106) is recorded as an order of its own.
ALTER TABLE orders DROP CONSTRAINT orders_order_type_check;
ALTER TABLE orders ADD CONSTRAINT orders_order_type_check
    CHECK (order_type IN ('FOK', 'FAK', 'GTC', 'GTD', 'SETTLE'));

-- +goose Down
DELETE FROM orders WHERE order_type = 'SETTLE';
ALTER TABLE orders DROP CONSTRAINT orders_order_type_check;
ALTER TABLE orders ADD CONSTRAINT orders_order_type_check
    CHECK (order_type IN ('FOK', 'FAK', 'GTC', 'GTD'));
ALTER TABLE paper_accounts DROP COLUMN reset_at;
