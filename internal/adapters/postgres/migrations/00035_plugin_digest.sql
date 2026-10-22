-- +goose Up
-- The SHA-256 of each version's checksums.txt, in hex: its files as installed, which its
-- approval holds to (P16); '' for a version recorded before, until it is next opened.
ALTER TABLE plugin_versions ADD COLUMN digest text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE plugin_versions DROP COLUMN digest;
