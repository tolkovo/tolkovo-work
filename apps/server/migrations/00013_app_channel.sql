-- The «Приложение» channel (AP-01, docs/02 §11.6). Owned by internal/channels.
--
-- A company's own desktop or mobile app sends problem reports with logs. The
-- app carries an ingest key, which only creates reports and is extractable
-- from the app: the database keeps its SHA-256, never the key. The secret
-- that signs the app user's identity lives sealed in channel_secrets, the
-- limits in channels.config.

-- +goose Up

ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check
    CHECK (type IN ('telegram_bot', 'telegram_user', 'app'));

CREATE TABLE ingest_keys (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    channel_id    uuid NOT NULL,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    -- SHA-256 of the whole key. The key holds 32 random bytes, so a fast
    -- hash is enough: there is nothing to guess.
    key_hash      bytea NOT NULL UNIQUE CHECK (octet_length(key_hash) = 32),
    -- The first characters of the key, to tell keys apart in the panel.
    prefix        text NOT NULL CHECK (char_length(prefix) BETWEEN 1 AND 32),
    created_by    uuid,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz,
    revoked_at    timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, created_by) REFERENCES memberships (workspace_id, user_id) ON DELETE SET NULL (created_by)
);

CREATE INDEX ingest_keys_channel_idx ON ingest_keys (workspace_id, channel_id, created_at);

ALTER TABLE ingest_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE ingest_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON ingest_keys
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

-- Keys are revoked, never deleted: the panel shows when a key stopped.
GRANT SELECT, INSERT, UPDATE ON ingest_keys TO tolkovo_app;

-- +goose Down
DROP TABLE ingest_keys;
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
-- NOT VALID: app channels already created stay, with their conversations.
ALTER TABLE channels ADD CONSTRAINT channels_type_check
    CHECK (type IN ('telegram_bot', 'telegram_user')) NOT VALID;
