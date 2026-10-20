-- +goose Up
-- A user's kill switch (D220): set while their workflows place no live orders. And an
-- admin's caps for them (D222): NULL is the instance's, 0 none, else whole dollars.
ALTER TABLE users
    ADD COLUMN live_stopped_at timestamptz,
    ADD COLUMN max_live_order  integer CHECK (max_live_order >= 0),
    ADD COLUMN max_live_day    integer CHECK (max_live_day >= 0);

-- What a user's live buys spent and hold, for the day cap (D223).
CREATE INDEX orders_live_by_owner ON orders (owner_id, created_at) WHERE mode = 'live';

-- +goose Down
DROP INDEX orders_live_by_owner;

ALTER TABLE users
    DROP COLUMN live_stopped_at,
    DROP COLUMN max_live_order,
    DROP COLUMN max_live_day;
