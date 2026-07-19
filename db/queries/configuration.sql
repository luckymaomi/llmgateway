-- name: CreateConfigRevision :one
INSERT INTO config_revisions (document, checksum, created_by)
VALUES (sqlc.arg(document), sqlc.arg(checksum), sqlc.arg(created_by)) RETURNING *;

-- name: GetConfigRevision :one
SELECT * FROM config_revisions WHERE id = sqlc.arg(id);

-- name: ListConfigRevisions :many
SELECT * FROM config_revisions ORDER BY revision DESC LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: GetActiveConfig :one
SELECT r.*, a.version AS active_version, a.updated_at AS active_updated_at
FROM active_config a JOIN config_revisions r ON r.id = a.revision_id WHERE a.singleton = true;

-- name: LockActiveConfig :one
SELECT revision_id, version FROM active_config WHERE singleton = true FOR UPDATE;

-- name: InitializeActiveConfig :execrows
INSERT INTO active_config (singleton, revision_id, version) VALUES (true, sqlc.arg(revision_id), 1) ON CONFLICT DO NOTHING;

-- name: PublishConfigRevision :execrows
UPDATE active_config SET revision_id = sqlc.arg(revision_id), version = version + 1, updated_at = now()
WHERE singleton = true AND version = sqlc.arg(expected_version);

-- name: MarkConfigPublished :exec
UPDATE config_revisions SET published_at = now(), published_by = sqlc.arg(published_by) WHERE id = sqlc.arg(id);

-- name: CreateConfigOutbox :exec
INSERT INTO config_outbox (revision_id, active_version, document)
VALUES (sqlc.arg(revision_id), sqlc.arg(active_version), sqlc.arg(document));

-- name: ListPendingConfigOutbox :many
SELECT * FROM config_outbox WHERE delivered_at IS NULL ORDER BY id LIMIT sqlc.arg(batch_size);

-- name: MarkConfigOutboxDelivered :exec
UPDATE config_outbox SET delivered_at = now(), attempts = attempts + 1, last_error = NULL WHERE id = sqlc.arg(id);

-- name: MarkConfigOutboxFailed :exec
UPDATE config_outbox SET attempts = attempts + 1, last_error = sqlc.arg(last_error) WHERE id = sqlc.arg(id);
