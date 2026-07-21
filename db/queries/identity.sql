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

-- name: GetUserForAdministrativeRecovery :one
SELECT * FROM users WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateUserPassword :one
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ClaimMemberPasswordResetMutation :one
INSERT INTO member_password_reset_mutations (
  actor_user_id, idempotency_key, user_id, request_fingerprint, request_id
) VALUES (
  sqlc.arg(actor_user_id), sqlc.arg(idempotency_key), sqlc.arg(user_id), sqlc.arg(request_fingerprint), sqlc.arg(request_id)
)
ON CONFLICT (actor_user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetMemberPasswordResetMutation :one
SELECT * FROM member_password_reset_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteMemberPasswordResetMutation :one
UPDATE member_password_reset_mutations
SET result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListUserDisplayNames :many
SELECT id, display_name
FROM users
WHERE id = ANY(sqlc.arg(user_ids)::uuid[])
ORDER BY id;

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
INSERT INTO invitations (code_digest, code_prefix, created_by, expires_at)
VALUES (sqlc.arg(code_digest), sqlc.arg(code_prefix), sqlc.arg(created_by), sqlc.arg(expires_at))
RETURNING *;

-- name: ClaimInvitationMutation :one
INSERT INTO invitation_mutations (actor_user_id, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetInvitationMutation :one
SELECT * FROM invitation_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteInvitationMutation :one
UPDATE invitation_mutations
SET invitation_id = sqlc.arg(invitation_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) AND invitation_id IS NULL
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

-- name: RevokeUserSessions :execrows
UPDATE sessions SET revoked_at = now() WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: RevokeUserSessionsExcept :execrows
UPDATE sessions SET revoked_at = now()
WHERE user_id = sqlc.arg(user_id) AND id <> sqlc.arg(preserved_session_id) AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at < now() - interval '1 day' OR revoked_at < now() - interval '1 day';

-- name: CreateGatewayKey :one
INSERT INTO gateway_keys (user_id, name, prefix, secret_digest, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(name), sqlc.arg(prefix), sqlc.arg(secret_digest), sqlc.narg(expires_at))
RETURNING *;

-- name: ClaimGatewayKeyMutation :one
INSERT INTO gateway_key_mutations (actor_user_id, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetGatewayKeyMutation :one
SELECT * FROM gateway_key_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteGatewayKeyMutation :one
UPDATE gateway_key_mutations
SET gateway_key_id = sqlc.arg(gateway_key_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetUserForGatewayKeyCreation :one
SELECT * FROM users WHERE id = sqlc.arg(id) FOR SHARE;

-- name: GetModelForGatewayKeyBinding :one
SELECT model.model_id AS id, model.public_name
FROM active_config active
JOIN config_revision_models model ON model.revision_id = active.revision_id
WHERE active.singleton = true AND model.model_id = sqlc.arg(id)
  AND EXISTS (
    SELECT 1
    FROM config_revision_routes route
    WHERE route.revision_id = active.revision_id
      AND route.model_id = model.model_id
  )
FOR SHARE OF active;

-- name: BindGatewayKeyModel :exec
INSERT INTO gateway_key_models (gateway_key_id, model_id)
VALUES (sqlc.arg(gateway_key_id), sqlc.arg(model_id));

-- name: ListGatewayKeyModelBindingsByUser :many
SELECT gkm.gateway_key_id, gkm.model_id, published.public_name
FROM gateway_key_models gkm
JOIN gateway_keys k ON k.id = gkm.gateway_key_id
JOIN LATERAL (
  SELECT model.public_name
  FROM config_revision_models model
  JOIN config_revisions revision ON revision.id = model.revision_id
  WHERE model.model_id = gkm.model_id AND revision.published_at IS NOT NULL
  ORDER BY revision.revision DESC
  LIMIT 1
) published ON true
WHERE k.user_id = sqlc.arg(user_id)
ORDER BY gkm.gateway_key_id, published.public_name, gkm.model_id;

-- name: IsGatewayKeyAuthorizedForModel :one
SELECT EXISTS (
  SELECT 1 FROM gateway_key_models
  WHERE gateway_key_id = sqlc.arg(gateway_key_id) AND model_id = sqlc.arg(model_id)
);

-- name: GetGatewayKeyByDigest :one
SELECT k.*, u.status AS user_status, u.role AS user_role
FROM gateway_keys k
JOIN users u ON u.id = k.user_id
WHERE k.secret_digest = sqlc.arg(secret_digest)
  AND k.revoked_at IS NULL
  AND (k.expires_at IS NULL OR k.expires_at > now());

-- name: GetGatewayKeyPrincipalByID :one
SELECT k.*, u.status AS user_status, u.role AS user_role
FROM gateway_keys k
JOIN users u ON u.id = k.user_id
WHERE k.id = sqlc.arg(id)
  AND k.revoked_at IS NULL
  AND (k.expires_at IS NULL OR k.expires_at > now());

-- name: ListGatewayKeysByUser :many
SELECT id, user_id, name, prefix, expires_at, revoked_at, last_used_at, created_at
FROM gateway_keys WHERE user_id = sqlc.arg(user_id) ORDER BY created_at DESC, id;

-- name: GetGatewayKeyForRevocation :one
SELECT id, user_id, revoked_at
FROM gateway_keys
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: GetGatewayKeyForReplacement :one
SELECT id, user_id, name, prefix, expires_at, revoked_at, last_used_at, created_at
FROM gateway_keys
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
FOR UPDATE;

-- name: ListGatewayKeyModelBindingsByKey :many
SELECT gkm.model_id, published.public_name
FROM gateway_key_models gkm
JOIN LATERAL (
  SELECT model.public_name
  FROM config_revision_models model
  JOIN config_revisions revision ON revision.id = model.revision_id
  WHERE model.model_id = gkm.model_id AND revision.published_at IS NOT NULL
  ORDER BY revision.revision DESC
  LIMIT 1
) published ON true
WHERE gkm.gateway_key_id = sqlc.arg(gateway_key_id)
ORDER BY published.public_name, gkm.model_id;

-- name: GetGatewayKeyRevocationState :one
SELECT user_id, revoked_at
FROM gateway_keys
WHERE id = sqlc.arg(id);

-- name: MarkGatewayKeyRevoked :execrows
UPDATE gateway_keys SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: TouchGatewayKey :exec
UPDATE gateway_keys SET last_used_at = now() WHERE id = sqlc.arg(id);
