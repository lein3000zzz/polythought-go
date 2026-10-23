-- +goose Up
-- Invites used more than once (D371, D393): how many sign-ups each allows and has had,
-- its code sealed with the server's key so admins can copy it again, revoking, and who
-- signed up with it. An invite made before keeps no code: only its hash was kept.
ALTER TABLE invites
    ADD COLUMN code_sealed bytea,
    ADD COLUMN max_uses    integer NOT NULL DEFAULT 1 CHECK (max_uses BETWEEN 1 AND 1000),
    ADD COLUMN uses        integer NOT NULL DEFAULT 0,
    ADD COLUMN revoked_at  timestamptz,
    ADD COLUMN revoked_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD CONSTRAINT invites_uses CHECK (uses BETWEEN 0 AND max_uses);

CREATE TABLE invite_uses (
    invite_id uuid        NOT NULL REFERENCES invites (id) ON DELETE CASCADE,
    user_id   uuid        REFERENCES users (id) ON DELETE SET NULL,
    used_at   timestamptz NOT NULL
);

CREATE INDEX invite_uses_invite ON invite_uses (invite_id, used_at);

INSERT INTO invite_uses (invite_id, user_id, used_at)
SELECT id, used_by, used_at FROM invites WHERE used_at IS NOT NULL;

UPDATE invites SET uses = 1 WHERE used_at IS NOT NULL;

ALTER TABLE invites DROP COLUMN used_by, DROP COLUMN used_at;

-- +goose Down
ALTER TABLE invites
    ADD COLUMN used_by uuid REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN used_at timestamptz;

-- Back to one use each: the first.
UPDATE invites i SET used_by = u.user_id, used_at = u.used_at
FROM (SELECT DISTINCT ON (invite_id) invite_id, user_id, used_at
      FROM invite_uses ORDER BY invite_id, used_at) u
WHERE u.invite_id = i.id;

DROP TABLE invite_uses;

ALTER TABLE invites
    DROP CONSTRAINT invites_uses,
    DROP COLUMN revoked_by,
    DROP COLUMN revoked_at,
    DROP COLUMN uses,
    DROP COLUMN max_uses,
    DROP COLUMN code_sealed;
