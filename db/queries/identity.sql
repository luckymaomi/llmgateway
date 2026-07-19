-- name: IsBootstrapped :one
SELECT CAST(bootstrapped_at IS NOT NULL AS boolean) AS bootstrapped FROM system_state WHERE singleton = true;

-- name: MarkBootstrapped :execrows
UPDATE system_state SET bootstrapped_at = now() WHERE singleton = true AND bootstrapped_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash, role, status, approved_at)
VALUES (lower(sqlc.arg(email)), sqlc.arg(display_name), sqlc.arg(password_hash), sqlc.arg(role), sqlc.arg(status), sqlc.narg(approved_at))
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email));

-- name: ListUsers :many
SELECT * FROM users
WHERE (sqlc.narg(status)::user_status IS NULL OR status = sqlc.narg(status))
ORDER BY created_at DESC, id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountUsers :one
SELECT count(*) FROM users WHERE (sqlc.narg(status)::user_status IS NULL OR status = sqlc.narg(status));

-- name: UpdateUserStatus :one
UPDATE users
SET status = sqlc.arg(status),
    approved_at = CASE WHEN sqlc.arg(status)::user_status = 'active' THEN coalesce(approved_at, now()) ELSE approved_at END,
    disabled_at = CASE WHEN sqlc.arg(status)::user_status = 'disabled' THEN now() ELSE NULL END,
    updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateInvitation :one
INSERT INTO invitations (code_digest, created_by, role, expires_at)
VALUES (sqlc.arg(code_digest), sqlc.arg(created_by), sqlc.arg(role), sqlc.arg(expires_at))
RETURNING *;

-- name: GetInvitationByDigestForUpdate :one
SELECT * FROM invitations WHERE code_digest = sqlc.arg(code_digest) FOR UPDATE;

-- name: ClaimInvitation :execrows
UPDATE invitations
SET claimed_by = sqlc.arg(claimed_by), claimed_at = now()
WHERE id = sqlc.arg(id) AND claimed_at IS NULL AND revoked_at IS NULL AND expires_at > now();

-- name: ListInvitations :many
SELECT * FROM invitations ORDER BY created_at DESC, id LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: RevokeInvitation :execrows
UPDATE invitations SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND claimed_at IS NULL;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_digest, csrf_digest, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token_digest), sqlc.arg(csrf_digest), sqlc.arg(expires_at))
RETURNING *;

-- name: GetSessionByDigest :one
SELECT s.*, u.email, u.display_name, u.role, u.status AS user_status
FROM sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_digest = sqlc.arg(token_digest)
  AND s.revoked_at IS NULL
  AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeUserSessions :exec
UPDATE sessions SET revoked_at = now() WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() - interval '1 day' OR revoked_at < now() - interval '1 day';

-- name: CreateGatewayKey :one
INSERT INTO gateway_keys (user_id, name, prefix, secret_digest, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(prefix), sqlc.arg(secret_digest), sqlc.narg(expires_at))
RETURNING *;

-- name: GetGatewayKeyByDigest :one
SELECT k.*, u.status AS user_status, u.role AS user_role
FROM gateway_keys k
JOIN users u ON u.id = k.user_id
WHERE k.secret_digest = sqlc.arg(secret_digest)
  AND k.revoked_at IS NULL
  AND (k.expires_at IS NULL OR k.expires_at > now());

-- name: ListGatewayKeysByUser :many
SELECT id, user_id, name, prefix, expires_at, revoked_at, last_used_at, created_at
FROM gateway_keys WHERE user_id = sqlc.arg(user_id) ORDER BY created_at DESC, id;

-- name: RevokeGatewayKey :execrows
UPDATE gateway_keys SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeOwnedGatewayKey :execrows
UPDATE gateway_keys SET revoked_at = now() WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: TouchGatewayKey :exec
UPDATE gateway_keys SET last_used_at = now() WHERE id = sqlc.arg(id);

-- name: AuthorizeUserModel :exec
INSERT INTO model_authorizations (user_id, model_id) VALUES (sqlc.arg(user_id), sqlc.arg(model_id)) ON CONFLICT DO NOTHING;

-- name: RevokeUserModel :execrows
DELETE FROM model_authorizations WHERE user_id = sqlc.arg(user_id) AND model_id = sqlc.arg(model_id);

-- name: IsUserAuthorizedForModel :one
SELECT EXISTS (SELECT 1 FROM model_authorizations WHERE user_id = sqlc.arg(user_id) AND model_id = sqlc.arg(model_id));
