-- Provider/model rows are deterministic projections of the validated code catalog.
-- name: UpsertProviderProjection :one
INSERT INTO providers (catalog_id, slug, name, kind, base_url, source_url, verified_at)
VALUES (sqlc.arg(catalog_id), sqlc.arg(slug), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(base_url), sqlc.arg(source_url), sqlc.arg(verified_at))
ON CONFLICT (catalog_id) DO UPDATE
SET slug = excluded.slug, name = excluded.name, kind = excluded.kind, base_url = excluded.base_url,
    source_url = excluded.source_url, verified_at = excluded.verified_at, updated_at = now()
RETURNING *;

-- name: UpsertModelProjection :one
INSERT INTO models (provider_id, public_name, upstream_name, display_name, capabilities)
VALUES (sqlc.arg(provider_id), sqlc.arg(public_name), sqlc.arg(upstream_name), sqlc.arg(display_name), sqlc.arg(capabilities))
ON CONFLICT (public_name) DO UPDATE
SET provider_id = excluded.provider_id, upstream_name = excluded.upstream_name,
    display_name = excluded.display_name, capabilities = excluded.capabilities, updated_at = now()
RETURNING *;

-- name: GetProviderByCatalogID :one
SELECT * FROM providers WHERE catalog_id = sqlc.arg(catalog_id);

-- name: GetProvider :one
SELECT * FROM providers WHERE id = sqlc.arg(id);

-- name: ListProviders :many
SELECT provider.*,
       (SELECT count(*) FROM resource_pools pool WHERE pool.provider_id = provider.id AND pool.status <> 'retired') AS resource_pool_count,
       (SELECT count(*) FROM provider_credentials credential
        JOIN resource_pools pool ON pool.id = credential.resource_pool_id
        WHERE pool.provider_id = provider.id AND credential.status = 'active') AS active_credential_count
FROM providers provider ORDER BY provider.name, provider.id;

-- name: GetModelByPublicName :one
SELECT model.*, provider.slug AS provider_slug, provider.kind AS provider_kind
FROM models model JOIN providers provider ON provider.id = model.provider_id
WHERE model.public_name = sqlc.arg(public_name);

-- name: GetModelForCredentialBinding :one
SELECT model.* FROM models model
JOIN resource_pool_models pool_model ON pool_model.model_id = model.id
WHERE model.id = sqlc.arg(id) AND pool_model.resource_pool_id = sqlc.arg(resource_pool_id)
FOR SHARE OF model;

-- name: ListModels :many
SELECT model.*, provider.slug AS provider_slug, provider.name AS provider_name
FROM models model JOIN providers provider ON provider.id = model.provider_id
ORDER BY model.public_name, model.id;

-- name: ClaimResourcePoolMutation :one
INSERT INTO resource_pool_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetResourcePoolMutation :one
SELECT * FROM resource_pool_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteResourcePoolMutation :one
UPDATE resource_pool_mutations SET resource_pool_id = sqlc.arg(resource_pool_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: CreateResourcePool :one
INSERT INTO resource_pools (provider_id, slug, name)
VALUES (sqlc.arg(provider_id), sqlc.arg(slug), sqlc.arg(name)) RETURNING *;

-- name: BindResourcePoolModel :exec
INSERT INTO resource_pool_models (resource_pool_id, model_id)
VALUES (sqlc.arg(resource_pool_id), sqlc.arg(model_id));

-- name: GetResourcePoolForUpdate :one
SELECT * FROM resource_pools WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateResourcePool :one
UPDATE resource_pools
SET name = sqlc.arg(name), updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: SetResourcePoolStatus :one
UPDATE resource_pools
SET status = sqlc.arg(status),
    retired_at = CASE WHEN sqlc.arg(status)::resource_pool_status = 'retired' THEN now() ELSE NULL END,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: GetResourcePool :one
SELECT pool.*, provider.catalog_id, provider.slug AS provider_slug, provider.name AS provider_name,
       provider.kind AS provider_kind, provider.base_url AS provider_base_url,
       provider.source_url AS provider_source_url, provider.verified_at AS provider_verified_at
FROM resource_pools pool JOIN providers provider ON provider.id = pool.provider_id
WHERE pool.id = sqlc.arg(id);

-- name: ListResourcePools :many
SELECT pool.*, provider.catalog_id, provider.slug AS provider_slug, provider.name AS provider_name,
       provider.kind AS provider_kind, provider.base_url AS provider_base_url,
       provider.source_url AS provider_source_url, provider.verified_at AS provider_verified_at,
       (SELECT count(*) FROM resource_pool_models model WHERE model.resource_pool_id = pool.id) AS model_count,
       (SELECT count(*) FROM provider_credentials credential WHERE credential.resource_pool_id = pool.id AND credential.status <> 'retired') AS credential_count,
       (SELECT count(*) FROM provider_credentials credential WHERE credential.resource_pool_id = pool.id AND credential.status = 'active') AS active_credential_count
FROM resource_pools pool JOIN providers provider ON provider.id = pool.provider_id
WHERE (sqlc.arg(include_retired)::boolean OR pool.status <> 'retired')
ORDER BY CASE pool.status WHEN 'active' THEN 0 WHEN 'disabled' THEN 1 ELSE 2 END, pool.name, pool.id;

-- name: ListResourcePoolModels :many
SELECT model.*, provider.slug AS provider_slug, provider.name AS provider_name
FROM resource_pool_models pool_model
JOIN models model ON model.id = pool_model.model_id
JOIN providers provider ON provider.id = model.provider_id
WHERE pool_model.resource_pool_id = sqlc.arg(resource_pool_id)
ORDER BY model.public_name, model.id;

-- name: CreateCredential :one
INSERT INTO provider_credentials (id, resource_pool_id, name, encrypted_secret, rpm_limit, tpm_limit, concurrency_limit)
VALUES (sqlc.arg(id), sqlc.arg(resource_pool_id), sqlc.arg(name), sqlc.arg(encrypted_secret), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit), sqlc.narg(concurrency_limit))
RETURNING *;

-- name: ClaimCredentialMutation :one
INSERT INTO credential_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetCredentialMutation :one
SELECT * FROM credential_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteCredentialMutation :one
UPDATE credential_mutations SET credential_id = sqlc.arg(credential_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id) RETURNING *;

-- name: GetCredentialForUpdate :one
SELECT * FROM provider_credentials WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateCredential :one
UPDATE provider_credentials
SET name = sqlc.arg(name),
    encrypted_secret = CASE WHEN sqlc.arg(replace_secret)::boolean THEN sqlc.arg(encrypted_secret) ELSE encrypted_secret END,
    rpm_limit = sqlc.narg(rpm_limit), tpm_limit = sqlc.narg(tpm_limit), concurrency_limit = sqlc.narg(concurrency_limit),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: SetCredentialStatus :one
UPDATE provider_credentials
SET status = sqlc.arg(status),
    cooldown_until = CASE WHEN sqlc.arg(status)::credential_status = 'active' THEN NULL ELSE cooldown_until END,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' AND sqlc.arg(status)::credential_status <> 'retired'
  AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: RetireCredential :one
UPDATE provider_credentials
SET status = 'retired', encrypted_secret = sqlc.arg(encrypted_tombstone), cooldown_until = NULL,
    retired_at = now(), updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: DeleteCredentialModelBindings :exec
DELETE FROM credential_models WHERE credential_id = sqlc.arg(credential_id);

-- name: BindCredentialModel :exec
INSERT INTO credential_models (credential_id, model_id, priority, weight)
VALUES (sqlc.arg(credential_id), sqlc.arg(model_id), sqlc.arg(priority), sqlc.arg(weight));

-- name: ListCredentialModelBindings :many
SELECT binding.credential_id, binding.model_id, model.public_name AS model_name, binding.priority, binding.weight
FROM credential_models binding JOIN models model ON model.id = binding.model_id
WHERE binding.credential_id = sqlc.arg(credential_id)
ORDER BY model.public_name, binding.model_id;

-- name: RecordCredentialProbe :one
UPDATE provider_credentials
SET last_probe_at = sqlc.arg(last_probe_at), last_probe_latency_ms = sqlc.arg(last_probe_latency_ms),
    last_probe_kind = sqlc.arg(last_probe_kind), last_probe_status = sqlc.arg(last_probe_status),
    last_probe_error_kind = sqlc.narg(last_probe_error_kind),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND status <> 'retired' RETURNING *;

-- name: RecordCredentialRuntimeSuccess :exec
UPDATE provider_credentials
SET status = 'active', cooldown_until = NULL, consecutive_failures = 0,
    last_success_at = sqlc.arg(observed_at), last_error_kind = NULL
WHERE id = sqlc.arg(id) AND status IN ('active', 'cooling');

-- name: RecordCredentialRuntimeFailure :exec
UPDATE provider_credentials
SET status = 'cooling', cooldown_until = sqlc.narg(cooldown_until),
    consecutive_failures = consecutive_failures + 1, last_error_kind = sqlc.arg(error_kind)
WHERE id = sqlc.arg(id) AND status IN ('active', 'cooling');

-- name: GetCredential :one
SELECT credential.*, pool.provider_id, pool.name AS resource_pool_name, pool.slug AS resource_pool_slug,
       provider.name AS provider_name, provider.kind AS provider_kind, provider.base_url AS provider_base_url
FROM provider_credentials credential
JOIN resource_pools pool ON pool.id = credential.resource_pool_id
JOIN providers provider ON provider.id = pool.provider_id
WHERE credential.id = sqlc.arg(id);

-- name: GetEncryptedCredential :one
SELECT encrypted_secret FROM provider_credentials WHERE id = sqlc.arg(id) AND status <> 'retired';

-- name: ListCredentials :many
SELECT credential.*, pool.provider_id, pool.name AS resource_pool_name, pool.slug AS resource_pool_slug,
       provider.name AS provider_name, provider.kind AS provider_kind, provider.base_url AS provider_base_url,
       recent.terminal_count, recent.completed_count, recent.last_checked_unix_seconds,
       recent.first_byte_p95_ms, recent.total_latency_p95_ms
FROM provider_credentials credential
JOIN resource_pools pool ON pool.id = credential.resource_pool_id
JOIN providers provider ON provider.id = pool.provider_id
LEFT JOIN LATERAL (
  SELECT count(*) FILTER (WHERE attempt.status IN ('completed', 'failed', 'uncertain')) AS terminal_count,
         count(*) FILTER (WHERE attempt.status = 'completed') AS completed_count,
         COALESCE(extract(epoch FROM max(COALESCE(attempt.completed_at, attempt.first_byte_at, attempt.sent_at, attempt.created_at))
           FILTER (WHERE attempt.status IN ('completed', 'failed', 'uncertain'))), -1)::bigint AS last_checked_unix_seconds,
         COALESCE((percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (attempt.first_byte_at - attempt.sent_at)) * 1000)
           FILTER (WHERE attempt.sent_at IS NOT NULL AND attempt.first_byte_at IS NOT NULL))::bigint, -1)::bigint AS first_byte_p95_ms,
         COALESCE((percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM (attempt.completed_at - attempt.sent_at)) * 1000)
           FILTER (WHERE attempt.sent_at IS NOT NULL AND attempt.completed_at IS NOT NULL))::bigint, -1)::bigint AS total_latency_p95_ms
  FROM request_attempts attempt
  WHERE attempt.credential_id = credential.id AND attempt.created_at >= now() - interval '24 hours'
) recent ON true
WHERE (sqlc.arg(include_retired)::boolean OR credential.status <> 'retired')
ORDER BY CASE credential.status WHEN 'active' THEN 0 WHEN 'cooling' THEN 1 WHEN 'disabled' THEN 2 ELSE 3 END,
         credential.created_at DESC, credential.id;

-- name: ListAvailableModelsForKey :many
SELECT DISTINCT model.id, model.public_name, model.upstream_name, model.capabilities, model.created_at,
       provider.id AS provider_id, provider.slug AS provider_slug, provider.kind AS provider_kind,
       provider.base_url AS provider_base_url
FROM gateway_key_models key_model
JOIN gateway_keys key ON key.id = key_model.gateway_key_id
JOIN models model ON model.id = key_model.model_id
JOIN providers provider ON provider.id = model.provider_id
WHERE key_model.gateway_key_id = sqlc.arg(gateway_key_id)
  AND key.revoked_at IS NULL AND (key.expires_at IS NULL OR key.expires_at > now())
  AND EXISTS (
    SELECT 1 FROM subscriptions subscription
    JOIN service_plan_version_routes route ON route.service_plan_version_id = subscription.service_plan_version_id
    JOIN resource_pools pool ON pool.id = route.resource_pool_id
    WHERE subscription.user_id = key.user_id AND route.model_id = model.id
      AND subscription.status = 'active' AND subscription.starts_at <= now() AND subscription.expires_at > now()
      AND pool.status = 'active'
  )
ORDER BY model.public_name, model.id;

-- name: ResolveAvailableModelForKey :one
SELECT model.id, model.public_name, model.upstream_name, model.capabilities, model.created_at,
       provider.id AS provider_id, provider.slug AS provider_slug, provider.kind AS provider_kind,
       provider.base_url AS provider_base_url,
       EXISTS (
         SELECT 1 FROM gateway_key_models key_model
         WHERE key_model.gateway_key_id = sqlc.arg(gateway_key_id) AND key_model.model_id = model.id
       ) AS key_authorized
FROM models model JOIN providers provider ON provider.id = model.provider_id
WHERE model.public_name = sqlc.arg(public_name);

-- name: ListResourcePoolCandidates :many
SELECT credential.id, binding.priority, binding.weight,
       credential.rpm_limit, credential.tpm_limit, credential.concurrency_limit,
       credential.consecutive_failures, credential.last_success_at, credential.cooldown_until
FROM provider_credentials credential
JOIN credential_models binding ON binding.credential_id = credential.id
JOIN resource_pools pool ON pool.id = credential.resource_pool_id
WHERE credential.resource_pool_id = sqlc.arg(resource_pool_id) AND binding.model_id = sqlc.arg(model_id)
  AND pool.status = 'active' AND credential.status IN ('active', 'cooling')
ORDER BY binding.priority, credential.id;
