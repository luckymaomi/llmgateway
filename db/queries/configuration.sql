-- name: CreateConfigRevision :one
INSERT INTO config_revisions (checksum, created_by)
VALUES (sqlc.arg(checksum), sqlc.arg(created_by)) RETURNING *;

-- name: ClaimConfigMutation :one
INSERT INTO config_mutations (actor_user_id, action, idempotency_key, request_fingerprint, request_id)
VALUES (sqlc.arg(actor_user_id), sqlc.arg(action), sqlc.arg(idempotency_key), sqlc.arg(request_fingerprint), sqlc.arg(request_id))
ON CONFLICT (actor_user_id, action, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetConfigMutation :one
SELECT * FROM config_mutations
WHERE actor_user_id = sqlc.arg(actor_user_id)
  AND action = sqlc.arg(action)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: CompleteConfigMutation :one
UPDATE config_mutations
SET revision_id = sqlc.arg(revision_id), result = sqlc.arg(result)
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: ListRegistryProvidersForSnapshot :many
SELECT id, slug, name, kind, base_url
FROM providers
WHERE enabled
ORDER BY id;

-- name: ListRegistryModelsForSnapshot :many
SELECT m.id, m.provider_id, m.public_name, m.upstream_name, m.display_name, m.resource_domain, m.capabilities, m.created_at
FROM models m
JOIN providers p ON p.id = m.provider_id
WHERE p.enabled
  AND m.enabled
  AND EXISTS (
    SELECT 1
    FROM credential_models cm
    JOIN provider_credentials credential ON credential.id = cm.credential_id
    WHERE cm.model_id = m.id
      AND credential.status <> 'disabled'
      AND credential.provider_id = m.provider_id
      AND credential.resource_domain = m.resource_domain
  )
ORDER BY m.id;

-- name: ListRegistryCredentialsForSnapshot :many
SELECT c.id, c.provider_id, c.resource_domain, c.rpm_limit, c.tpm_limit, c.concurrency_limit
FROM provider_credentials c
JOIN providers p ON p.id = c.provider_id
WHERE p.enabled
  AND c.status <> 'disabled'
  AND EXISTS (
    SELECT 1
    FROM credential_models cm
    JOIN models m ON m.id = cm.model_id
    WHERE cm.credential_id = c.id
      AND m.enabled
      AND m.provider_id = c.provider_id
      AND m.resource_domain = c.resource_domain
  )
ORDER BY c.id;

-- name: ListRegistryRoutesForSnapshot :many
SELECT cm.model_id, cm.credential_id, cm.priority, cm.weight
FROM credential_models cm
JOIN models m ON m.id = cm.model_id
JOIN providers p ON p.id = m.provider_id
JOIN provider_credentials c ON c.id = cm.credential_id
WHERE p.enabled
  AND m.enabled
  AND c.status <> 'disabled'
  AND c.provider_id = m.provider_id
  AND c.resource_domain = m.resource_domain
ORDER BY cm.model_id, cm.credential_id;

-- name: CreateConfigRevisionProvider :exec
INSERT INTO config_revision_providers (revision_id, provider_id, slug, name, kind, base_url)
VALUES (sqlc.arg(revision_id), sqlc.arg(provider_id), sqlc.arg(slug), sqlc.arg(name), sqlc.arg(kind), sqlc.arg(base_url));

-- name: CreateConfigRevisionModel :exec
INSERT INTO config_revision_models (revision_id, model_id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, created_at)
VALUES (sqlc.arg(revision_id), sqlc.arg(model_id), sqlc.arg(provider_id), sqlc.arg(public_name), sqlc.arg(upstream_name), sqlc.arg(display_name), sqlc.arg(resource_domain), sqlc.arg(capabilities), sqlc.arg(created_at));

-- name: CreateConfigRevisionCredential :exec
INSERT INTO config_revision_credentials (revision_id, credential_id, provider_id, resource_domain, rpm_limit, tpm_limit, concurrency_limit)
VALUES (sqlc.arg(revision_id), sqlc.arg(credential_id), sqlc.arg(provider_id), sqlc.arg(resource_domain), sqlc.narg(rpm_limit), sqlc.narg(tpm_limit), sqlc.narg(concurrency_limit));

-- name: CreateConfigRevisionRoute :exec
INSERT INTO config_revision_routes (revision_id, model_id, credential_id, priority, weight)
VALUES (sqlc.arg(revision_id), sqlc.arg(model_id), sqlc.arg(credential_id), sqlc.arg(priority), sqlc.arg(weight));

-- name: GetConfigRevision :one
SELECT * FROM config_revisions WHERE id = sqlc.arg(id);

-- name: GetConfigRevisionCatalogSummary :one
SELECT
  (SELECT count(*) FROM config_revision_providers provider WHERE provider.revision_id = sqlc.arg(target_revision_id)) AS provider_count,
  (SELECT count(*) FROM config_revision_models model WHERE model.revision_id = sqlc.arg(target_revision_id)) AS model_count,
  (SELECT count(*) FROM config_revision_credentials credential WHERE credential.revision_id = sqlc.arg(target_revision_id)) AS credential_count,
  (SELECT count(*) FROM config_revision_routes route WHERE route.revision_id = sqlc.arg(target_revision_id)) AS route_count;

-- name: ListConfigRevisionProviders :many
SELECT provider_id, slug, name, kind, base_url
FROM config_revision_providers
WHERE revision_id = sqlc.arg(revision_id)
ORDER BY provider_id;

-- name: ListConfigRevisionModels :many
SELECT model_id, provider_id, public_name, upstream_name, display_name, resource_domain, capabilities, created_at
FROM config_revision_models
WHERE revision_id = sqlc.arg(revision_id)
ORDER BY model_id;

-- name: ListConfigRevisionCredentials :many
SELECT credential_id, provider_id, resource_domain, rpm_limit, tpm_limit, concurrency_limit
FROM config_revision_credentials
WHERE revision_id = sqlc.arg(revision_id)
ORDER BY credential_id;

-- name: ListConfigRevisionRoutes :many
SELECT model_id, credential_id, priority, weight
FROM config_revision_routes
WHERE revision_id = sqlc.arg(revision_id)
ORDER BY model_id, credential_id;

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
UPDATE config_revisions
SET published_at = COALESCE(published_at, now()),
    published_by = COALESCE(published_by, sqlc.arg(published_by))
WHERE id = sqlc.arg(id);
