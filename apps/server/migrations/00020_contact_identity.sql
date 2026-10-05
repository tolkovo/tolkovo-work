-- Unverified identity of a contact (W-04, docs/02 §13.2). Owned by
-- internal/conversations.
--
-- A verified contact is the one with external_id: the company's backend
-- signed that id (site widget or app report). A visitor or app user may
-- also claim an id, a name or an email without a valid signature; then
-- the contact is a new one, never merged with others, and its name and
-- email are only what was typed. identity_unverified marks such a contact
-- so the panel says «личность не подтверждена» and nothing ever merges or
-- matches contacts by those values. Contacts that claimed nothing
-- (Telegram, created by an operator, an anonymous visitor) have neither.

-- +goose Up

ALTER TABLE contacts
    ADD COLUMN identity_unverified boolean NOT NULL DEFAULT false,
    ADD CONSTRAINT contacts_identity_chk CHECK (NOT identity_unverified OR external_id IS NULL);

-- +goose Down

ALTER TABLE contacts
    DROP CONSTRAINT contacts_identity_chk,
    DROP COLUMN identity_unverified;
