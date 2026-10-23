-- +goose Up
-- Whether the user lets their agents see their workflows' personal settings, such as
-- webhook URLs (D401): off to start with, switched at a browser only.
ALTER TABLE users ADD COLUMN agents_see_personal boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users DROP COLUMN agents_see_personal;
