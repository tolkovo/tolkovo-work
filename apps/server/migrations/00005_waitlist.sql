-- Waitlist of the cloud beta with double opt-in (docs/05 §6, backlog L-01).
-- A global table without RLS, like users: entries belong to the project, not
-- to a workspace (docs/adr/0003-multitenancy-rls.md). Accessed through
-- db.InGlobalTx by the application role only.

-- +goose Up
CREATE TABLE waitlist_entries (
    id                      uuid PRIMARY KEY,
    -- Trimmed and lower-cased by the waitlist module; citext keeps the
    -- uniqueness case-insensitive even for rows written another way.
    email                   citext NOT NULL UNIQUE CHECK (char_length(email) BETWEEN 3 AND 254),
    locale                  text NOT NULL CHECK (locale IN ('ru', 'en')),
    channels                text[] NOT NULL DEFAULT '{}'
                            CHECK (channels <@ ARRAY['telegram', 'web', 'whatsapp', 'vk_max', 'email']),
    status                  text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed')),
    -- Version of the consent text the person agreed to (GDPR art. 7(1)).
    consent_version         text NOT NULL CHECK (char_length(consent_version) BETWEEN 1 AND 40),
    -- Tokens are 32 random bytes; only their SHA-256 is stored, so a database
    -- leak does not let anyone confirm or delete entries. Each email sent
    -- gets fresh tokens and the previous links stop working.
    confirm_token_hash      bytea UNIQUE CHECK (octet_length(confirm_token_hash) = 32),
    confirm_expires_at      timestamptz,
    unsubscribe_token_hash  bytea UNIQUE CHECK (octet_length(unsubscribe_token_hash) = 32),
    -- Emails sent for this entry; caps resends for one address.
    send_count              integer NOT NULL DEFAULT 0 CHECK (send_count >= 0),
    last_sent_at            timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    -- Bumped by every accepted submission; pending entries expire from it.
    updated_at              timestamptz NOT NULL DEFAULT now(),
    confirmed_at            timestamptz,
    CHECK ((confirm_token_hash IS NULL) = (confirm_expires_at IS NULL)),
    CHECK ((status = 'confirmed') = (confirmed_at IS NOT NULL))
);
-- The purge job scans pending entries by age and expired confirm tokens.
CREATE INDEX waitlist_entries_pending_idx ON waitlist_entries (updated_at) WHERE status = 'pending';
CREATE INDEX waitlist_entries_confirm_expiry_idx ON waitlist_entries (confirm_expires_at)
    WHERE confirm_expires_at IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON waitlist_entries TO tolkovo_app;

-- +goose Down
DROP TABLE waitlist_entries;
