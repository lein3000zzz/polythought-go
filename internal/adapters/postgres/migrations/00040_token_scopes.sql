-- +goose Up
-- An API token's scopes (D398), as an agent's grant has them: what it may do, chosen as it
-- is made. NULL, for the tokens made before, is every scope.
ALTER TABLE api_tokens ADD COLUMN scopes text[];

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN scopes;
