-- name: InsertReport :one
INSERT INTO app_reports (
    id, workspace_id, channel_id, ingest_key_id, status, token_hash, idempotency_key, request_hash, message,
    contact_external_id, contact_email, contact_name, contact_verified, app, total_size, retention_days, created_at
) VALUES ($1, $2, $3, $4, 'uploading', $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: InsertReportFile :one
INSERT INTO app_report_files (id, workspace_id, report_id, position, storage_key, file_name, content_type, size, sha256)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetReport :one
SELECT * FROM app_reports
WHERE workspace_id = $1 AND channel_id = $2 AND id = $3;

-- name: GetReportByIdempotencyKey :one
SELECT * FROM app_reports
WHERE workspace_id = $1 AND channel_id = $2 AND idempotency_key = $3;

-- name: ReportFiles :many
SELECT * FROM app_report_files
WHERE workspace_id = $1 AND report_id = $2
ORDER BY position;

-- name: ReplaceReportToken :execrows
-- Only while the files are still being uploaded.
UPDATE app_reports
SET token_hash = $3
WHERE workspace_id = $1 AND id = $2 AND status = 'uploading';

-- name: SubmitReport :execrows
UPDATE app_reports
SET status = 'submitted', submitted_at = $3
WHERE workspace_id = $1 AND id = $2 AND status = 'uploading';

-- name: GetReportForProcessing :one
SELECT * FROM app_reports
WHERE workspace_id = $1 AND id = $2
FOR UPDATE;

-- name: MarkProcessed :execrows
UPDATE app_reports
SET status = 'processed', conversation_id = @conversation_id, processed_at = @processed_at, files_expire_at = @files_expire_at
WHERE workspace_id = @workspace_id AND id = @id AND status = 'submitted';

-- name: ExpireReport :execrows
-- An unfinished report can no longer be completed.
UPDATE app_reports
SET status = 'expired'
WHERE workspace_id = @workspace_id AND id = @id AND status = 'uploading' AND created_at < @before;

-- name: DeleteExpiredReport :execrows
DELETE FROM app_reports
WHERE workspace_id = $1 AND id = $2 AND status = 'expired';

-- name: MarkFilesExpired :execrows
UPDATE app_reports
SET files_expired_at = @expired_at
WHERE workspace_id = @workspace_id AND id = @id AND status = 'processed' AND files_expired_at IS NULL;

-- Queries of the system role (policy system_cleanup): ids only.

-- name: UnfinishedReports :many
SELECT workspace_id, id FROM app_reports
WHERE status IN ('uploading', 'expired') AND created_at < @before
ORDER BY created_at
LIMIT @lim;

-- name: ReportsWithExpiredFiles :many
SELECT workspace_id, id FROM app_reports
WHERE status = 'processed' AND files_expired_at IS NULL AND files_expire_at < @now
ORDER BY files_expire_at
LIMIT @lim;
