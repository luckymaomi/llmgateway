-- name: CreateAuditEvent :one
INSERT INTO audit_events (actor_user_id, action, target_type, target_id, request_id, detail)
VALUES (sqlc.narg(actor_user_id), sqlc.arg(action), sqlc.arg(target_type), sqlc.narg(target_id), sqlc.narg(request_id), sqlc.arg(detail)) RETURNING *;

-- name: ListAuditEvents :many
SELECT * FROM audit_events ORDER BY created_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);
