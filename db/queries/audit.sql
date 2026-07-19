-- name: CreateAuditEvent :one
INSERT INTO audit_events (actor_user_id, action, target_type, target_id, request_id, detail)
VALUES (sqlc.narg(actor_user_id), sqlc.arg(action), sqlc.arg(target_type), sqlc.narg(target_id), sqlc.narg(request_id), sqlc.arg(detail)) RETURNING *;

-- name: ListAuditEvents :many
SELECT * FROM audit_events ORDER BY created_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: UpsertContentRecord :exec
INSERT INTO content_records (request_id, encrypted_content, expires_at)
VALUES (sqlc.arg(request_id), sqlc.arg(encrypted_content), sqlc.arg(expires_at))
ON CONFLICT (request_id) DO UPDATE SET encrypted_content = excluded.encrypted_content, expires_at = excluded.expires_at;

-- name: GetContentRecord :one
SELECT * FROM content_records WHERE request_id = sqlc.arg(request_id) AND expires_at > now();

-- name: DeleteExpiredContent :execrows
DELETE FROM content_records WHERE expires_at <= now();
