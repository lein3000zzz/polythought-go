-- +goose Up
-- Which Telegram user each account linked, and their private chat with the instance's bot.
-- A Telegram user links one account at a time.
CREATE TABLE telegram_links (
    user_id          uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    telegram_user_id bigint      NOT NULL UNIQUE,
    chat_id          bigint      NOT NULL,
    username         text        NOT NULL DEFAULT '',
    first_name       text        NOT NULL DEFAULT '',
    linked_at        timestamptz NOT NULL
);

-- Single-use codes in t.me/<bot>?start=<code> links, by their SHA-256. One per account.
CREATE TABLE telegram_link_codes (
    code_hash  bytea PRIMARY KEY,
    user_id    uuid        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL
);

-- +goose Down
DROP TABLE telegram_link_codes;
DROP TABLE telegram_links;
