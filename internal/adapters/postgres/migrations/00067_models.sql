-- +goose Up
-- Models: each user's named models, served by their workers, which take requests with a
-- worker key of the model's own, or called at a URL. A key is kept hashed, its secret shown
-- once as it is made.
CREATE TABLE models (
    id             uuid        PRIMARY KEY,
    owner_id       uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           text        NOT NULL,
    kind           text        NOT NULL, -- worker or url
    url            text        NOT NULL DEFAULT '',
    credential_id  uuid        REFERENCES credentials (id) ON DELETE SET NULL,
    last_answer_at timestamptz,
    last_error     text        NOT NULL DEFAULT '',
    last_error_at  timestamptz,
    created_at     timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    UNIQUE (owner_id, name)
);

CREATE TABLE model_keys (
    id           uuid        PRIMARY KEY,
    model_id     uuid        NOT NULL REFERENCES models (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    prefix       text        NOT NULL,
    hash         bytea       NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL,
    last_used_at timestamptz
);

CREATE INDEX model_keys_model ON model_keys (model_id);

-- +goose Down
DROP TABLE model_keys;
DROP TABLE models;
