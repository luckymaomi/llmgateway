#!/usr/bin/env bash
set -euo pipefail

[[ ( $# -eq 4 || $# -eq 5 ) && $4 == --confirm-new-database-restore &&
   ( $# -eq 4 || ${5:-} == --confirm-incomplete-database-replacement ) ]] || {
  echo "usage: $0 DEPLOYMENT_ENV VERIFIED_BACKUP_PAYLOAD TARGET_DATABASE --confirm-new-database-restore [--confirm-incomplete-database-replacement]" >&2
  exit 2
}
replace_incomplete_database=false
[[ $# -eq 4 ]] || replace_incomplete_database=true
[[ $EUID -eq 0 ]] || { echo "database restore requires UID 0" >&2; exit 1; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
deployment_environment=$(configured_path "recovered deployment environment" "$1")
[[ -f $deployment_environment && ! -L $deployment_environment && ${deployment_environment##*/} == deployment.env ]] || {
  echo "recovered deployment environment is missing or invalid" >&2
  exit 1
}
require_root_owned_path_ancestors "recovered deployment environment" "$deployment_environment"
configuration_directory=$(dirname -- "$deployment_environment")
verify_runtime_configuration_tree "$configuration_directory"
load_llmgateway_environment "$deployment_environment"
require_file_secrets
require_immutable_gateway_image
require_configuration_bindings "$configuration_directory"
export DEPLOY_DIRECTORY=${DEPLOY_DIRECTORY:-$SCRIPT_DIRECTORY}
payload_input=$(configured_path "verified backup payload" "$2")
[[ ! -L $payload_input ]] || { echo "backup payload must not be a symbolic link" >&2; exit 1; }
payload=$(realpath -e -- "$payload_input")
target_database=$3
[[ -d $payload && ! -L $payload ]] || { echo "verified backup payload is missing or invalid" >&2; exit 1; }
require_root_owned_path_ancestors "verified backup payload" "$payload"
[[ $target_database =~ ^[A-Za-z][A-Za-z0-9_]{2,62}$ && $target_database != postgres && $target_database != template* ]] || {
  echo "unsafe restore database name" >&2
  exit 1
}
: "${LLMGATEWAY_POSTGRES_USER:=llmgateway}"
configured_database=${LLMGATEWAY_POSTGRES_DB:-llmgateway}
[[ $target_database == "$configured_database" ]] || { echo "restore target must match the recovered deployment database" >&2; exit 1; }

created=false
lock_acquired=false
cleanup() {
  local status=$? cleanup_status
  trap - EXIT
  cleanup_status=$status
  if [[ $created == true ]]; then
    if ! deployment_compose exec -T postgres dropdb --if-exists --force --username "$LLMGATEWAY_POSTGRES_USER" "$target_database" >/dev/null 2>&1; then
      echo "failed to remove the incomplete restore database" >&2
      cleanup_status=1
    fi
  fi
  if [[ $lock_acquired == true ]] && ! release_llmgateway_maintenance_lock; then
    echo "failed to release the LLMGateway maintenance lock" >&2
    cleanup_status=1
  fi
  exit "$cleanup_status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

acquire_llmgateway_maintenance_lock postgres-restore
lock_acquired=true
verify_backup_payload "$payload"
dump=$payload/postgres.dump

compose_services_output=$(deployment_compose config --services)
[[ -n $compose_services_output ]] || { echo "recovered Compose project has no services" >&2; exit 1; }
mapfile -t compose_services <<< "$compose_services_output"
postgres_defined=false
for compose_service in "${compose_services[@]}"; do
  [[ $compose_service =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || { echo "recovered Compose project contains an invalid service name" >&2; exit 1; }
  if [[ $compose_service == postgres ]]; then
    postgres_defined=true
    continue
  fi
  service_containers_output=$(deployment_compose ps --all --quiet "$compose_service")
  service_containers=()
  if [[ -n $service_containers_output ]]; then mapfile -t service_containers <<< "$service_containers_output"; fi
  for service_container in "${service_containers[@]}"; do
    [[ $service_container =~ ^[a-f0-9]{12,64}$ ]] || { echo "Compose returned an invalid container ID" >&2; exit 1; }
    service_state=$(docker inspect --format '{{.State.Status}}' "$service_container")
    [[ $service_state == exited ]] || {
      echo "all recovered services except PostgreSQL must be exited before database restore" >&2
      exit 1
    }
  done
done
[[ $postgres_defined == true ]] || { echo "recovered Compose project has no PostgreSQL service" >&2; exit 1; }

postgres_containers_output=$(deployment_compose ps --all --quiet postgres)
postgres_containers=()
if [[ -n $postgres_containers_output ]]; then mapfile -t postgres_containers <<< "$postgres_containers_output"; fi
(( ${#postgres_containers[@]} <= 1 )) || { echo "multiple PostgreSQL containers belong to the recovered project" >&2; exit 1; }
postgres_state=absent
if (( ${#postgres_containers[@]} == 1 )); then
  [[ ${postgres_containers[0]} =~ ^[a-f0-9]{12,64}$ ]] || { echo "Compose returned an invalid PostgreSQL container ID" >&2; exit 1; }
  postgres_state=$(docker inspect --format '{{.State.Status}}' "${postgres_containers[0]}")
fi
case "$postgres_state" in
  running) ;;
  absent|created|exited)
  LLMGATEWAY_POSTGRES_DB=postgres
  export LLMGATEWAY_POSTGRES_DB
  deployment_compose up --detach --wait postgres
  LLMGATEWAY_POSTGRES_DB=$configured_database
  export LLMGATEWAY_POSTGRES_DB
  ;;
  *)
    echo "PostgreSQL container must be absent, created, exited, or running before restore" >&2
    exit 1
    ;;
esac
deployment_compose exec -T postgres pg_restore --list <"$dump" >/dev/null
existing=$(deployment_compose exec -T postgres psql --username "$LLMGATEWAY_POSTGRES_USER" --dbname postgres \
  --tuples-only --no-align --command "SELECT 1 FROM pg_database WHERE datname = '$target_database'")
if [[ $existing == 1 ]]; then
  [[ $replace_incomplete_database == true ]] || { echo "restore target database already exists" >&2; exit 1; }
  deployment_compose exec -T postgres dropdb --force --username "$LLMGATEWAY_POSTGRES_USER" "$target_database"
fi
deployment_compose exec -T postgres createdb --username "$LLMGATEWAY_POSTGRES_USER" "$target_database"
created=true
deployment_compose exec -T postgres pg_restore --exit-on-error --single-transaction --no-owner --no-privileges \
  --username "$LLMGATEWAY_POSTGRES_USER" --dbname "$target_database" <"$dump"
manifest_migration_version=$(awk -F= '$1 == "migration_version" { print $2 }' "$payload/backup-manifest")
restored_migration_version=$(deployment_compose exec -T postgres psql --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$target_database" --tuples-only --no-align \
  --command 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied')
[[ $restored_migration_version == "$manifest_migration_version" ]] || {
  echo "restored database migration version does not match the backup manifest" >&2
  exit 1
}
deployment_compose up --detach --no-deps --force-recreate --wait postgres
created=false
echo "PostgreSQL backup restored into new database $target_database."
