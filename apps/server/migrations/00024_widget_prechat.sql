-- The widget's pre-chat form. Owned by internal/widget.
--
-- A visitor who has no contact yet (no message sent) may still fill in
-- the form: what they typed waits in the session, as a name or an email
-- from the site's identify does, and goes onto the contact with the first
-- message. The consent to the processing of personal data (152-ФЗ, GDPR)
-- waits there too and lands in contacts.attributes.consent.

-- +goose Up

ALTER TABLE widget_sessions
    ADD COLUMN claim_phone text CHECK (char_length(claim_phone) BETWEEN 3 AND 32),
    ADD COLUMN claim_consent jsonb CHECK (jsonb_typeof(claim_consent) = 'object');

-- +goose Down

ALTER TABLE widget_sessions
    DROP COLUMN claim_consent,
    DROP COLUMN claim_phone;
