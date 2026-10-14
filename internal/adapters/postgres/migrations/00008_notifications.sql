-- +goose Up
-- The kinds of notification each user switched on or off. A kind without a row uses its
-- default, which lives in code with the kind.
CREATE TABLE notification_prefs (
    user_id uuid    NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind    text    NOT NULL,
    enabled boolean NOT NULL,
    PRIMARY KEY (user_id, kind)
);

-- +goose Down
DROP TABLE notification_prefs;
