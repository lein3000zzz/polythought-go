-- +goose Up
-- A key may hold several members (D121): the entries Throttle counts (D143), or the
-- traders Consensus has seen. One alone, as Only new keeps them, is the empty member.
ALTER TABLE node_memory ADD COLUMN member text NOT NULL DEFAULT '';
ALTER TABLE node_memory DROP CONSTRAINT node_memory_pkey;
ALTER TABLE node_memory ADD PRIMARY KEY (workflow_id, node_id, key, member);

-- +goose Down
DELETE FROM node_memory WHERE member <> '';
ALTER TABLE node_memory DROP CONSTRAINT node_memory_pkey;
ALTER TABLE node_memory ADD PRIMARY KEY (workflow_id, node_id, key);
ALTER TABLE node_memory DROP COLUMN member;
