-- name: ClaimProviderMutation :one
INSERT INTO provider_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetProviderMutation :one
SELECT * FROM provider_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id)
  AND action = sqlc.arg(action)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteProviderMutation :one
UPDATE provider_mutations
SET provider_id = sqlc.arg(provider_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: CreateProvider :one
INSERT INTO providers (slug, name, kind, base_url, enabled, source_url, verified_at)
VALUES (sqlc.arg(slug), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(base_url), sqlc.arg(enabled), sqlc.narg(source_url), sqlc.narg(verified_at))
RETURNING *;

-- name: UpdateProvider :one
UPDATE providers
SET name = sqlc.arg(name),
    kind = sqlc.arg(kind),
    base_url = sqlc.arg(base_url),
    verified_at = CASE
        WHEN kind IS DISTINCT FROM sqlc.arg(kind) OR base_url IS DISTINCT FROM sqlc.arg(base_url) THEN NULL
        ELSE verified_at
    END,
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) RETURNING *;

-- name: SetProviderEnabled :one
UPDATE providers SET enabled = sqlc.arg(enabled), updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) RETURNING *;

-- name: GetProvider :one
SELECT * FROM providers WHERE id = sqlc.arg(id);

-- name: GetProviderForUpdate :one
SELECT * FROM providers WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: ListProviders :many
SELECT * FROM providers ORDER BY name, id;

-- name: CreateModel :one
INSERT INTO models (provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, enabled)
VALUES (sqlc.arg(provider_id), sqlc.arg(public_name), sqlc.arg(upstream_name), sqlc.arg(display_name), sqlc.arg(resource_domain), sqlc.arg(capabilities), sqlc.arg(enabled))
RETURNING *;

-- name: UpdateModel :one
UPDATE models SET public_name = sqlc.arg(public_name), upstream_name = sqlc.arg(upstream_name), display_name = sqlc.arg(display_name), resource_domain = sqlc.arg(resource_domain), capabilities = sqlc.arg(capabilities), enabled = sqlc.arg(enabled), updated_at = now()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: GetModelByPublicName :one
SELECT m.*, p.slug AS provider_slug, p.kind AS provider_kind, p.base_url AS provider_base_url, p.enabled AS provider_enabled
FROM models m JOIN providers p ON p.id = m.provider_id WHERE m.public_name = sqlc.arg(public_name);

-- name: ListModels :many
SELECT m.*, p.slug AS provider_slug, p.name AS provider_name
FROM models m JOIN providers p ON p.id = m.provider_id ORDER BY m.public_name, m.id;

-- name: ListPublishedModelsForKey :many
SELECT ac.revision_id,
       m.model_id AS id, m.public_name, m.upstream_name, m.resource_domain, m.capabilities, m.created_at,
       p.provider_id, p.slug AS provider_slug, p.kind AS provider_kind, p.base_url AS provider_base_url
FROM active_config ac
JOIN config_revision_models m ON m.revision_id = ac.revision_id
JOIN config_revision_providers p ON p.revision_id = m.revision_id AND p.provider_id = m.provider_id
JOIN gateway_key_models key_model ON key_model.model_id = m.model_id
WHERE ac.singleton = true
  AND key_model.gateway_key_id = sqlc.arg(gateway_key_id)
  AND EXISTS (
    SELECT 1
    FROM config_revision_routes route
    JOIN config_revision_credentials credential
      ON credential.revision_id = route.revision_id AND credential.credential_id = route.credential_id
    JOIN provider_credentials live_credential ON live_credential.id = credential.credential_id
    WHERE route.revision_id = ac.revision_id
      AND route.model_id = m.model_id
      AND (
        live_credential.status = 'active'
        OR (live_credential.status = 'cooling' AND live_credential.cooldown_until <= now())
      )
  )
ORDER BY m.public_name, m.model_id;

-- name: ResolvePublishedModelForKey :one
SELECT ac.revision_id,
       m.model_id AS id, m.public_name, m.upstream_name, m.resource_domain, m.capabilities, m.created_at,
       p.provider_id, p.slug AS provider_slug, p.kind AS provider_kind, p.base_url AS provider_base_url,
       EXISTS (
         SELECT 1 FROM gateway_key_models key_model
         WHERE key_model.gateway_key_id = sqlc.arg(gateway_key_id) AND key_model.model_id = m.model_id
       ) AS authorized
FROM active_config ac
JOIN config_revision_models m ON m.revision_id = ac.revision_id
JOIN config_revision_providers p ON p.revision_id = m.revision_id AND p.provider_id = m.provider_id
WHERE ac.singleton = true
  AND m.public_name = sqlc.arg(public_name);

-- name: CreateCredential :one
INSERT INTO provider_credentials (id, provider_id, name, encrypted_secret, resource_domain, rpm_limit, tpm_limit, concurrency_limit)
VALUES (sqlc.arg(id), sqlc.arg(provider_id), sqlc.arg(name), sqlc.arg(encrypted_secret), sqlc.arg(resource_domain), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit), sqlc.narg(concurrency_limit))
RETURNING *;

-- name: ClaimCredentialMutation :one
INSERT INTO credential_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetCredentialMutation :one
SELECT * FROM credential_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id) AND action = sqlc.arg(action) AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteCredentialMutation :one
UPDATE credential_mutations
SET credential_id = sqlc.arg(credential_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetModelForCredentialBinding :one
SELECT * FROM models WHERE id = sqlc.arg(id) FOR SHARE;

-- name: GetCredentialForUpdate :one
SELECT * FROM provider_credentials WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: UpdateCredential :one
UPDATE provider_credentials
SET name = sqlc.arg(name),
    encrypted_secret = CASE WHEN sqlc.arg(replace_secret)::boolean THEN sqlc.arg(encrypted_secret) ELSE encrypted_secret END,
    resource_domain = sqlc.arg(resource_domain),
    rpm_limit = sqlc.narg(rpm_limit),
    tpm_limit = sqlc.narg(tpm_limit),
    concurrency_limit = sqlc.narg(concurrency_limit),
    updated_at = GREATEST(now(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: SetCredentialStatus :one
UPDATE provider_credentials
SET status = sqlc.arg(status),
    cooldown_until = CASE WHEN sqlc.arg(status)::credential_status = 'active' THEN NULL ELSE cooldown_until END,
    updated_at = GREATEST(now(), updated_at + interval '1 microsecond')
WHERE id = sqlc.arg(id) AND updated_at = sqlc.arg(expected_updated_at)
RETURNING *;

-- name: DeleteCredentialModelBindings :exec
DELETE FROM credential_models WHERE credential_id = sqlc.arg(credential_id);

-- name: RecordCredentialProbe :one
UPDATE provider_credentials
SET last_probe_at = sqlc.arg(last_probe_at),
    last_probe_latency_ms = sqlc.arg(last_probe_latency_ms),
    last_probe_kind = sqlc.arg(last_probe_kind),
    last_probe_status = sqlc.arg(last_probe_status),
    last_probe_error_kind = sqlc.narg(last_probe_error_kind)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: UpdateCredentialState :one
UPDATE provider_credentials
SET status = sqlc.arg(status), cooldown_until = sqlc.narg(cooldown_until), consecutive_failures = sqlc.arg(consecutive_failures), last_success_at = sqlc.narg(last_success_at), last_error_kind = sqlc.narg(last_error_kind), updated_at = now()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: RecordCredentialRuntimeSuccess :exec
UPDATE provider_credentials
SET status = 'active', cooldown_until = NULL, consecutive_failures = 0,
    last_success_at = sqlc.arg(observed_at), last_error_kind = NULL
WHERE id = sqlc.arg(id) AND status <> 'disabled';

-- name: RecordCredentialRuntimeFailure :exec
UPDATE provider_credentials
SET status = 'cooling', cooldown_until = sqlc.narg(cooldown_until),
    consecutive_failures = consecutive_failures + 1,
    last_error_kind = sqlc.arg(error_kind)
WHERE id = sqlc.arg(id) AND status <> 'disabled';

-- name: ListCredentials :many
SELECT id, provider_id, name, resource_domain, status, rpm_limit, tpm_limit, concurrency_limit, cooldown_until, consecutive_failures, last_success_at, last_error_kind, last_probe_at, last_probe_latency_ms, last_probe_kind, last_probe_status, last_probe_error_kind, created_at, updated_at
FROM provider_credentials ORDER BY name, id;

-- name: GetCredentialSecret :one
SELECT * FROM provider_credentials WHERE id = sqlc.arg(id);

-- name: BindCredentialModel :exec
INSERT INTO credential_models (credential_id, model_id, priority, weight)
VALUES (sqlc.arg(credential_id), sqlc.arg(model_id), sqlc.arg(priority), sqlc.arg(weight))
ON CONFLICT (credential_id, model_id) DO UPDATE SET priority = excluded.priority, weight = excluded.weight;

-- name: ListCredentialModelBindings :many
SELECT cm.credential_id, m.id AS model_id, m.public_name, cm.priority, cm.weight
FROM credential_models cm
JOIN models m ON m.id = cm.model_id
ORDER BY cm.credential_id, m.public_name, m.id;

-- name: ListCredentialModelBindingsForCredential :many
SELECT cm.credential_id, m.id AS model_id, m.public_name, cm.priority, cm.weight
FROM credential_models cm
JOIN models m ON m.id = cm.model_id
WHERE cm.credential_id = sqlc.arg(credential_id)
ORDER BY m.public_name, m.id;

-- name: ListPublishedCandidates :many
SELECT route.credential_id AS id, route.priority, route.weight,
       credential.rpm_limit, credential.tpm_limit, credential.concurrency_limit,
       live_credential.consecutive_failures, live_credential.last_success_at, live_credential.cooldown_until
FROM config_revision_routes route
JOIN config_revision_credentials credential
  ON credential.revision_id = route.revision_id AND credential.credential_id = route.credential_id
JOIN provider_credentials live_credential ON live_credential.id = credential.credential_id
WHERE route.revision_id = sqlc.arg(revision_id)
  AND route.model_id = sqlc.arg(model_id)
  AND credential.resource_domain = sqlc.arg(resource_domain)
  AND (
    live_credential.status = 'active'
    OR (live_credential.status = 'cooling' AND live_credential.cooldown_until <= now())
  )
ORDER BY route.priority, route.credential_id;
