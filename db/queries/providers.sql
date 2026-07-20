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

-- name: ListAuthorizedModels :many
SELECT m.*, p.slug AS provider_slug, p.name AS provider_name
FROM models m
JOIN providers p ON p.id = m.provider_id
JOIN model_authorizations a ON a.model_id = m.id
WHERE a.user_id = sqlc.arg(user_id) AND m.enabled AND p.enabled
ORDER BY m.public_name, m.id;

-- name: CreateCredential :one
INSERT INTO provider_credentials (id, provider_id, name, encrypted_secret, resource_domain, rpm_limit, tpm_limit, concurrency_limit, fixed_proxy_url)
VALUES (sqlc.arg(id), sqlc.arg(provider_id), sqlc.arg(name), sqlc.arg(encrypted_secret), sqlc.arg(resource_domain), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit), sqlc.narg(concurrency_limit), sqlc.narg(fixed_proxy_url))
RETURNING *;

-- name: UpdateCredentialState :one
UPDATE provider_credentials
SET status = sqlc.arg(status), cooldown_until = sqlc.narg(cooldown_until), consecutive_failures = sqlc.arg(consecutive_failures), last_success_at = sqlc.narg(last_success_at), last_error_kind = sqlc.narg(last_error_kind), updated_at = now()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: ListCredentials :many
SELECT id, provider_id, name, resource_domain, status, rpm_limit, tpm_limit, concurrency_limit, fixed_proxy_url, cooldown_until, consecutive_failures, last_success_at, last_error_kind, created_at, updated_at
FROM provider_credentials ORDER BY name, id;

-- name: GetCredentialSecret :one
SELECT * FROM provider_credentials WHERE id = sqlc.arg(id);

-- name: BindCredentialModel :exec
INSERT INTO credential_models (credential_id, model_id, priority, weight)
VALUES (sqlc.arg(credential_id), sqlc.arg(model_id), sqlc.arg(priority), sqlc.arg(weight))
ON CONFLICT (credential_id, model_id) DO UPDATE SET priority = excluded.priority, weight = excluded.weight;

-- name: ListEligibleCredentials :many
SELECT c.*, cm.priority, cm.weight
FROM provider_credentials c
JOIN credential_models cm ON cm.credential_id = c.id
WHERE cm.model_id = sqlc.arg(model_id)
  AND c.resource_domain = sqlc.arg(resource_domain)
  AND c.status <> 'disabled'
  AND (c.cooldown_until IS NULL OR c.cooldown_until <= now())
ORDER BY cm.priority, c.id;
