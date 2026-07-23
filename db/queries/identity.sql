-- name: IsBootstrapped :one
SELECT CAST(bootstrapped_at IS NOT NULL AS boolean) AS bootstrapped FROM system_state WHERE singleton = true;

-- name: MarkBootstrapped :execrows
UPDATE system_state SET bootstrapped_at = now() WHERE singleton = true AND bootstrapped_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (email, display_name, password_hash, role, status)
VALUES (lower(sqlc.arg(email)), sqlc.arg(display_name), sqlc.arg(password_hash), sqlc.arg(role), sqlc.arg(status))
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = sqlc.arg(id);

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(sqlc.arg(email)) AND status <> 'deleted';

-- name: GetUserForAdministrativeRecovery :one
SELECT * FROM users WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateUserPassword :one
UPDATE users SET password_hash = sqlc.arg(password_hash), updated_at = now()
WHERE id = sqlc.arg(id) AND status <> 'deleted'
RETURNING *;

-- name: UpdateOwnPassword :execrows
UPDATE users SET password_hash = sqlc.arg(replacement_password_hash), updated_at = now()
WHERE id = sqlc.arg(id) AND password_hash = sqlc.arg(expected_password_hash) AND status = 'active';

-- name: ClaimMemberMutation :one
INSERT INTO member_mutations (actor_user_id, action, idempotency_key, user_id, request_fingerprint, request_id, encrypted_one_time_secret)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.narg(user_id), sqlc.arg(request_fingerprint), sqlc.arg(request_id), sqlc.narg(encrypted_one_time_secret))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetMemberMutation :one
SELECT * FROM member_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteMemberMutation :one
UPDATE member_mutations
SET user_id = sqlc.arg(user_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListUserDisplayNames :many
SELECT id, display_name FROM users WHERE id = ANY(sqlc.arg(user_ids)::uuid[]) ORDER BY id;

-- name: ListUsers :many
SELECT * FROM users
WHERE status <> 'deleted'
  AND (sqlc.narg(status)::user_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', email, display_name) ILIKE '%' || sqlc.arg(search)::text || '%')
ORDER BY created_at DESC, id
LIMIT sqlc.arg(page_size) OFFSET sqlc.arg(page_offset);

-- name: CountUsers :one
SELECT count(*) FROM users
WHERE status <> 'deleted'
  AND (sqlc.narg(status)::user_status IS NULL OR status = sqlc.narg(status))
  AND (sqlc.arg(search)::text = '' OR concat_ws(' ', email, display_name) ILIKE '%' || sqlc.arg(search)::text || '%');

-- name: UpdateUserProfile :one
UPDATE users
SET email = lower(sqlc.arg(email)), display_name = sqlc.arg(display_name),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND role = 'member' AND status <> 'deleted' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: UpdateUserStatus :one
UPDATE users
SET status = sqlc.arg(status),
    disabled_at = CASE WHEN sqlc.arg(status)::user_status = 'disabled' THEN now() ELSE NULL END,
    deleted_at = NULL,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND role = 'member' AND status <> 'deleted'
RETURNING *;

-- name: MarkUserDeleted :one
UPDATE users
SET status = 'deleted', disabled_at = NULL, deleted_at = now(),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND role = 'member' AND status <> 'deleted'
RETURNING *;

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_digest, csrf_digest, expires_at)
VALUES (sqlc.arg(user_id), sqlc.arg(token_digest), sqlc.arg(csrf_digest), sqlc.arg(expires_at))
RETURNING *;

-- name: GetSessionByDigest :one
SELECT s.*, u.email, u.display_name, u.role, u.status AS user_status
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_digest = sqlc.arg(token_digest) AND s.revoked_at IS NULL AND s.expires_at > now();

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
UPDATE sessions SET revoked_at = now() WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: RevokeGatewayKeysForUser :execrows
UPDATE gateway_keys SET revoked_at = now() WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

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
UPDATE gateway_key_mutations SET gateway_key_id = sqlc.arg(gateway_key_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: GetUserForGatewayKeyCreation :one
SELECT * FROM users WHERE id = sqlc.arg(id) AND status = 'active' FOR SHARE;

-- name: GetModelForGatewayKeyBinding :one
SELECT model.id, model.public_name
FROM models model
WHERE model.id = sqlc.arg(id)
  AND EXISTS (
    SELECT 1
    FROM subscriptions subscription
    JOIN service_plan_versions version ON version.id = subscription.service_plan_version_id
    JOIN service_plan_version_routes route ON route.service_plan_version_id = version.id AND route.model_id = model.id
    WHERE subscription.user_id = sqlc.arg(user_id)
      AND subscription.status = 'active'
      AND subscription.starts_at <= now() AND subscription.expires_at > now()
  )
FOR SHARE;

-- name: BindGatewayKeyModel :exec
INSERT INTO gateway_key_models (gateway_key_id, model_id) VALUES (sqlc.arg(gateway_key_id), sqlc.arg(model_id));

-- name: ListGatewayKeyModelBindingsByUser :many
SELECT gkm.gateway_key_id, gkm.model_id, model.public_name
FROM gateway_key_models gkm
JOIN gateway_keys key ON key.id = gkm.gateway_key_id
JOIN models model ON model.id = gkm.model_id
WHERE key.user_id = sqlc.arg(user_id)
ORDER BY gkm.gateway_key_id, model.public_name, gkm.model_id;

-- name: IsGatewayKeyAuthorizedForModel :one
SELECT EXISTS (SELECT 1 FROM gateway_key_models WHERE gateway_key_id = sqlc.arg(gateway_key_id) AND model_id = sqlc.arg(model_id));

-- name: GetGatewayKeyByDigest :one
SELECT key.*, member.status AS user_status, member.role AS user_role
FROM gateway_keys key JOIN users member ON member.id = key.user_id
WHERE key.secret_digest = sqlc.arg(secret_digest) AND key.revoked_at IS NULL
  AND (key.expires_at IS NULL OR key.expires_at > now());

-- name: GetGatewayKeyPrincipalByID :one
SELECT key.*, member.status AS user_status, member.role AS user_role
FROM gateway_keys key JOIN users member ON member.id = key.user_id
WHERE key.id = sqlc.arg(id) AND key.revoked_at IS NULL
  AND (key.expires_at IS NULL OR key.expires_at > now());

-- name: ListGatewayKeysByUser :many
SELECT id, user_id, name, prefix, expires_at, revoked_at, last_used_at, created_at
FROM gateway_keys WHERE user_id = sqlc.arg(user_id) ORDER BY created_at DESC, id;

-- name: GetGatewayKeyForRevocation :one
SELECT id, user_id, revoked_at FROM gateway_keys WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: GetGatewayKeyForReplacement :one
SELECT id, user_id, name, prefix, expires_at, revoked_at, last_used_at, created_at
FROM gateway_keys
WHERE id = sqlc.arg(id) AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())
FOR UPDATE;

-- name: ListGatewayKeyModelBindingsByKey :many
SELECT gkm.model_id, model.public_name
FROM gateway_key_models gkm JOIN models model ON model.id = gkm.model_id
WHERE gkm.gateway_key_id = sqlc.arg(gateway_key_id)
ORDER BY model.public_name, gkm.model_id;

-- name: GetGatewayKeyRevocationState :one
SELECT user_id, revoked_at FROM gateway_keys WHERE id = sqlc.arg(id);

-- name: MarkGatewayKeyRevoked :execrows
UPDATE gateway_keys SET revoked_at = now() WHERE id = sqlc.arg(id) AND revoked_at IS NULL;

-- name: TouchGatewayKey :exec
UPDATE gateway_keys SET last_used_at = now() WHERE id = sqlc.arg(id);
