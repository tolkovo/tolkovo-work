-- Telegram messages about conversations (docs/06 I-06).
-- Owned by internal/notifications.
--
-- The workspace's notifications bot (TK-02) also tells a member that a
-- conversation was given to them, that the customer wrote in one of theirs
-- and that a customer's conversation waits in the queue, while their panel
-- is closed. Each kind can be turned off by the member.

-- +goose Up

ALTER TABLE notification_prefs
    ADD COLUMN tg_assigned boolean NOT NULL DEFAULT true,
    ADD COLUMN tg_messages boolean NOT NULL DEFAULT true,
    ADD COLUMN tg_queue boolean NOT NULL DEFAULT true;

-- +goose Down

ALTER TABLE notification_prefs
    DROP COLUMN tg_assigned,
    DROP COLUMN tg_messages,
    DROP COLUMN tg_queue;
