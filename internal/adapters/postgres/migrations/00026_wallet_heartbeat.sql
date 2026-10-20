-- +goose Up
-- Whether Polymarket cancels the wallet's resting orders when the server goes quiet
-- (D214): heartbeats, off until its owner turns them on.
ALTER TABLE wallets ADD COLUMN heartbeat boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE wallets DROP COLUMN heartbeat;
