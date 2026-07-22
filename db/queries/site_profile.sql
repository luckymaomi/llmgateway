-- name: GetSiteProfile :one
SELECT * FROM site_profile WHERE singleton = true;

-- name: UpdateSiteProfile :one
UPDATE site_profile
SET name = sqlc.arg(name),
    description = sqlc.arg(description),
    contact = sqlc.arg(contact),
    version = version + 1,
    updated_by = sqlc.arg(updated_by),
    updated_at = GREATEST(clock_timestamp(), updated_at + interval '1 microsecond')
WHERE singleton = true AND version = sqlc.arg(expected_version)
RETURNING *;
