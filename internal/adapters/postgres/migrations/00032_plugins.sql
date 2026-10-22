-- +goose Up
-- The plugins admins installed (D321), each version's files on disk (D315). A plugin
-- runs its approved version, when enabled, from the next start (D317); a newer version
-- waits beside it until an admin approves it (D318).
CREATE TABLE plugins (
    id         text        PRIMARY KEY,
    version    text, -- the approved version, the one that runs; NULL until the first approval
    enabled    boolean     NOT NULL DEFAULT true,
    everyone   boolean     NOT NULL DEFAULT true, -- false: only the users of plugin_users (D282)
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

-- Each version installed, with its manifest: what it brings, and the permissions it
-- asks for, which an approval grants.
CREATE TABLE plugin_versions (
    plugin_id    text        NOT NULL REFERENCES plugins (id) ON DELETE CASCADE,
    version      text        NOT NULL,
    manifest     jsonb       NOT NULL,
    signer       text        NOT NULL DEFAULT '', -- the trusted key's name; '' for none an admin trusts
    key_id       text        NOT NULL DEFAULT '', -- the signing key's ID; '' unsigned
    installed_by uuid REFERENCES users (id) ON DELETE SET NULL,
    installed_at timestamptz NOT NULL,
    approved_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    approved_at  timestamptz,
    PRIMARY KEY (plugin_id, version)
);

ALTER TABLE plugins ADD FOREIGN KEY (id, version) REFERENCES plugin_versions (plugin_id, version)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE plugin_users (
    plugin_id text NOT NULL REFERENCES plugins (id) ON DELETE CASCADE,
    user_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (plugin_id, user_id)
);

-- Everything admins did with plugins, kept after a plugin is gone.
CREATE TABLE plugin_audit (
    id        uuid PRIMARY KEY, -- UUIDv7, so IDs sort by time
    plugin_id text        NOT NULL,
    version   text        NOT NULL DEFAULT '',
    action    text        NOT NULL,
    detail    text        NOT NULL DEFAULT '',
    actor_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    at        timestamptz NOT NULL
);

CREATE INDEX plugin_audit_plugin ON plugin_audit (plugin_id, at DESC);

-- +goose Down
DROP TABLE plugin_audit;
DROP TABLE plugin_users;
ALTER TABLE plugins DROP CONSTRAINT plugins_id_version_fkey;
DROP TABLE plugin_versions;
DROP TABLE plugins;
