-- Reports from company apps (AP-02, docs/02 §11.6). Owned by internal/ingest.
--
-- An app sends a report by its ingest key: text, who the user is, what the
-- app is, and the files it is about to upload. The report waits in
-- 'uploading' until the app has put every file by its signed link and calls
-- complete; then it is 'submitted' and becomes a conversation (AP-03).
-- The report token lets the app read the replies (AP-06); only its SHA-256
-- is kept.

-- +goose Up

CREATE TABLE app_reports (
    id                   uuid PRIMARY KEY,
    workspace_id         uuid NOT NULL,
    channel_id           uuid NOT NULL,
    ingest_key_id        uuid NOT NULL,
    status               text NOT NULL CHECK (status IN ('uploading', 'submitted')),
    token_hash           bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    -- A retry with the same key and body returns this report; the key is
    -- unique within the channel.
    idempotency_key      text CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    request_hash         bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    message              text NOT NULL CHECK (char_length(message) BETWEEN 1 AND 10000),
    -- Who the app user says they are. Personal data: never logged.
    contact_external_id  text CHECK (char_length(contact_external_id) <= 255),
    contact_email        text CHECK (char_length(contact_email) <= 254),
    contact_name         text CHECK (char_length(contact_name) <= 120),
    -- True only with an identity_token signed by the channel's secret.
    contact_verified     boolean NOT NULL,
    -- name, version, os, os_version, locale, extra.
    app                  jsonb NOT NULL DEFAULT '{}',
    total_size           bigint NOT NULL CHECK (total_size >= 0),
    created_at           timestamptz NOT NULL,
    submitted_at         timestamptz,
    UNIQUE (workspace_id, id),
    FOREIGN KEY (workspace_id, channel_id) REFERENCES channels (workspace_id, id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, ingest_key_id) REFERENCES ingest_keys (workspace_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX app_reports_idempotency_uq ON app_reports (workspace_id, channel_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
-- Unfinished reports are removed after an hour (AP-03).
CREATE INDEX app_reports_uploading_idx ON app_reports (created_at) WHERE status = 'uploading';

CREATE TABLE app_report_files (
    id            uuid PRIMARY KEY,
    workspace_id  uuid NOT NULL,
    report_id     uuid NOT NULL,
    -- Order in the request, so links come back in the same order.
    position      smallint NOT NULL CHECK (position BETWEEN 0 AND 19),
    storage_key   text NOT NULL UNIQUE,
    file_name     text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
    content_type  text NOT NULL CHECK (char_length(content_type) BETWEEN 1 AND 255),
    size          bigint NOT NULL CHECK (size > 0),
    sha256        bytea NOT NULL CHECK (octet_length(sha256) = 32),
    UNIQUE (workspace_id, report_id, position),
    FOREIGN KEY (workspace_id, report_id) REFERENCES app_reports (workspace_id, id) ON DELETE CASCADE
);

ALTER TABLE app_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app_reports
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

ALTER TABLE app_report_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE app_report_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON app_report_files
    USING (workspace_id = app_current_workspace())
    WITH CHECK (workspace_id = app_current_workspace());

GRANT SELECT, INSERT, UPDATE ON app_reports TO tolkovo_app;
GRANT SELECT, INSERT ON app_report_files TO tolkovo_app;

-- +goose Down
DROP TABLE app_report_files;
DROP TABLE app_reports;
