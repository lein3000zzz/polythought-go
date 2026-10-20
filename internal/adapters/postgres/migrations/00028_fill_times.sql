-- +goose Up
-- When each fill happened: a live trade's match time, or when a paper order filled. What a
-- day's spending counts is by it (R6); fills from before have none, and count from their
-- order's time.
ALTER TABLE order_fills ADD COLUMN filled_at timestamptz;

-- +goose Down
ALTER TABLE order_fills DROP COLUMN filled_at;
