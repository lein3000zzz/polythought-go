-- +goose Up
-- The Telegram message asking the user to confirm, with its buttons, to say there how it
-- was decided (D406); empty when none went.
ALTER TABLE confirmations ADD COLUMN telegram_message bigint NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE confirmations DROP COLUMN telegram_message;
