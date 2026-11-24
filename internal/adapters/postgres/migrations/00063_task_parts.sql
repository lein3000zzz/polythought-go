-- +goose Up
-- Agents' tasks paged (D436): a task answered per item, too big for an agent to take at
-- once, is split into parts, each handed to an agent on its own lease and answered alone;
-- the run carries on once every part is. A task that fits is one part. The lease moves from
-- the wait to its parts.
CREATE TABLE task_parts (
    execution_id uuid    NOT NULL REFERENCES waits (execution_id) ON DELETE CASCADE,
    part         integer NOT NULL,
    first_item   integer NOT NULL, -- of the task's items, the first in this part
    items        integer NOT NULL, -- and how many
    lease_holder text,
    lease_name   text,
    lease_until  timestamptz,
    answers      jsonb,            -- an answer per item, once answered
    answered_by  text    NOT NULL DEFAULT '',
    PRIMARY KEY (execution_id, part)
);

CREATE INDEX task_parts_lease_holder_idx ON task_parts (lease_holder);

-- The tasks waiting now: one part each, its lease as it was.
INSERT INTO task_parts (execution_id, part, first_item, items, lease_holder, lease_name, lease_until)
SELECT execution_id, 0, 0, COALESCE(jsonb_array_length(task -> 'items'), 0), lease_holder, lease_name, lease_until
FROM waits
WHERE task IS NOT NULL;

ALTER TABLE waits DROP COLUMN lease_holder, DROP COLUMN lease_name, DROP COLUMN lease_until;

-- +goose Down
ALTER TABLE waits ADD COLUMN lease_holder text, ADD COLUMN lease_name text, ADD COLUMN lease_until timestamptz;

UPDATE waits w
SET lease_holder = p.lease_holder, lease_name = p.lease_name, lease_until = p.lease_until
FROM task_parts p
WHERE p.execution_id = w.execution_id AND p.part = 0;

DROP TABLE task_parts;
