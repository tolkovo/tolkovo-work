-- Capacity of operators and distribution to the least loaded (ADR-0021,
-- step 1). Owned by internal/routing.
--
-- max_chats bounds the open conversations (assigned or pending) the router
-- gives a member: the workspace's limit, or the member's own over it. NULL
-- is no limit, as before. The load itself is counted from conversations,
-- not kept here.

-- +goose Up

ALTER TABLE routing_settings
    DROP CONSTRAINT routing_settings_mode_check,
    ADD CONSTRAINT routing_settings_mode_check CHECK (mode IN ('manual', 'round_robin', 'least_loaded')),
    ADD COLUMN max_chats smallint CHECK (max_chats BETWEEN 1 AND 50);

ALTER TABLE operator_states
    ADD COLUMN max_chats smallint CHECK (max_chats BETWEEN 1 AND 50);

-- +goose Down

ALTER TABLE operator_states DROP COLUMN max_chats;

UPDATE routing_settings SET mode = 'round_robin' WHERE mode = 'least_loaded';
ALTER TABLE routing_settings
    DROP COLUMN max_chats,
    DROP CONSTRAINT routing_settings_mode_check,
    ADD CONSTRAINT routing_settings_mode_check CHECK (mode IN ('manual', 'round_robin'));
