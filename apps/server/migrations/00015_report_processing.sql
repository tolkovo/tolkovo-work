-- Processing of app reports (AP-03, docs/02 §11.6). app_reports and the
-- system policy belong to internal/ingest, the attachments check to
-- internal/conversations.
--
-- A submitted report is turned into a conversation by the job
-- ingest.process; the report then is 'processed' and points to it. Reports
-- left in 'uploading' for an hour become 'expired' (complete no longer
-- works), their files are deleted, then the row. Files of processed
-- reports are deleted after the channel's retention, counted from
-- processing with the retention the channel had when the report arrived.

-- +goose Up

ALTER TABLE app_reports DROP CONSTRAINT app_reports_status_check;
ALTER TABLE app_reports ADD CONSTRAINT app_reports_status_check
    CHECK (status IN ('uploading', 'submitted', 'processed', 'expired'));
ALTER TABLE app_reports
    ADD COLUMN retention_days    smallint NOT NULL DEFAULT 30 CHECK (retention_days BETWEEN 1 AND 365),
    ADD COLUMN conversation_id   uuid,
    ADD COLUMN processed_at      timestamptz,
    ADD COLUMN files_expire_at   timestamptz,
    ADD COLUMN files_expired_at  timestamptz,
    ADD CONSTRAINT app_reports_conversation_fk
        FOREIGN KEY (workspace_id, conversation_id) REFERENCES conversations (workspace_id, id),
    ADD CONSTRAINT app_reports_processed_check
        CHECK ((status = 'processed') = (processed_at IS NOT NULL AND files_expire_at IS NOT NULL));
ALTER TABLE app_reports ALTER COLUMN retention_days DROP DEFAULT;

DROP INDEX app_reports_uploading_idx;
CREATE INDEX app_reports_unfinished_idx ON app_reports (created_at) WHERE status IN ('uploading', 'expired');
CREATE INDEX app_reports_files_expire_idx ON app_reports (files_expire_at)
    WHERE status = 'processed' AND files_expired_at IS NULL;

-- Attachments of a report point to the objects the app uploaded: no copy.
-- The check of 00004 got a generated name (attachments_check): found by
-- its definition.
-- +goose StatementBegin
DO $$
DECLARE c text;
BEGIN
    SELECT conname INTO STRICT c FROM pg_constraint
    WHERE conrelid = 'attachments'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%/conv/%';
    EXECUTE format('ALTER TABLE attachments DROP CONSTRAINT %I', c);
END $$;
-- +goose StatementEnd
ALTER TABLE attachments ADD CONSTRAINT attachments_storage_key_check
    CHECK (storage_key LIKE 'ws/' || workspace_id::text || '/conv/%'
        OR storage_key LIKE 'ws/' || workspace_id::text || '/reports/%');

-- The worker finds the reports to clean up across workspaces: ids and
-- times only, never the text or the contact. The cleanup itself runs in the
-- report's tenant transaction as tolkovo_app.
CREATE POLICY system_cleanup ON app_reports FOR SELECT TO tolkovo_system
    USING (status IN ('uploading', 'expired') OR (status = 'processed' AND files_expired_at IS NULL));
GRANT SELECT (id, workspace_id, status, created_at, files_expire_at, files_expired_at) ON app_reports TO tolkovo_system;

-- Expired reports are deleted; their files go by cascade.
GRANT DELETE ON app_reports TO tolkovo_app;

-- +goose Down
REVOKE DELETE ON app_reports FROM tolkovo_app;
REVOKE SELECT (id, workspace_id, status, created_at, files_expire_at, files_expired_at) ON app_reports FROM tolkovo_system;
DROP POLICY system_cleanup ON app_reports;
ALTER TABLE attachments DROP CONSTRAINT attachments_storage_key_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_storage_key_check
    CHECK (storage_key LIKE 'ws/' || workspace_id::text || '/conv/%');
DROP INDEX app_reports_files_expire_idx;
DROP INDEX app_reports_unfinished_idx;
CREATE INDEX app_reports_uploading_idx ON app_reports (created_at) WHERE status = 'uploading';
DELETE FROM app_reports WHERE status IN ('processed', 'expired');
ALTER TABLE app_reports
    DROP CONSTRAINT app_reports_processed_check,
    DROP CONSTRAINT app_reports_conversation_fk,
    DROP COLUMN files_expired_at,
    DROP COLUMN files_expire_at,
    DROP COLUMN processed_at,
    DROP COLUMN conversation_id,
    DROP COLUMN retention_days;
ALTER TABLE app_reports DROP CONSTRAINT app_reports_status_check;
ALTER TABLE app_reports ADD CONSTRAINT app_reports_status_check CHECK (status IN ('uploading', 'submitted'));
