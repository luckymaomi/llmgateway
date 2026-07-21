#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: upgrade-linux.sh NEW_IMAGE@sha256:DIGEST /absolute/backup.dump [https://health-url]" >&2
  exit 2
}

[[ $# -ge 2 && $# -le 3 ]] || usage
[[ $(id -u) -eq 0 ]] || { echo "upgrade requires root" >&2; exit 1; }
new_image=$1
backup_path=$2
health_url=${3:-}
[[ "$backup_path" == /* && ! -e "$backup_path" ]] || { echo "backup path must be absolute and absent" >&2; exit 1; }

DEPLOY_DIRECTORY=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$DEPLOY_DIRECTORY/lib.sh"
environment_file=/etc/llmgateway/deployment.env
load_llmgateway_environment "$environment_file"
require_file_secrets
require_immutable_gateway_image
old_image=$LLMGATEWAY_GATEWAY_IMAGE
[[ "$new_image" =~ @sha256:[a-f0-9]{64}$ ]] || { echo "new image must be an immutable sha256 reference" >&2; exit 1; }

lock_file=/run/lock/llmgateway-upgrade.lock
exec 9>"$lock_file"
flock -n 9 || { echo "another LLMGateway upgrade is running" >&2; exit 1; }

backup_directory=$(dirname -- "$backup_path")
mkdir -p "$backup_directory"
database_bytes=$(deployment_compose exec -T postgres psql --username "$LLMGATEWAY_POSTGRES_USER" --dbname "$LLMGATEWAY_POSTGRES_DB" --tuples-only --no-align --command 'SELECT pg_database_size(current_database())')
available_kib=$(df -Pk "$backup_directory" | awk 'NR==2 {print $4}')
[[ "$database_bytes" =~ ^[0-9]+$ && "$available_kib" =~ ^[0-9]+$ ]] || { echo "could not measure backup capacity" >&2; exit 1; }
(( available_kib * 1024 >= database_bytes * 2 )) || { echo "backup target has less than twice the database size available" >&2; exit 1; }

temporary_backup="$backup_path.partial"
trap 'rm -f -- "$temporary_backup"' EXIT
deployment_compose exec -T postgres pg_dump \
  --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" \
  --format custom --compress 9 > "$temporary_backup"
deployment_compose exec -T postgres pg_restore --list < "$temporary_backup" >/dev/null
mv -- "$temporary_backup" "$backup_path"

migration_version() {
  deployment_compose exec -T postgres psql \
    --username "$LLMGATEWAY_POSTGRES_USER" \
    --dbname "$LLMGATEWAY_POSTGRES_DB" \
    --tuples-only --no-align \
    --command 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied'
}

before_version=$(migration_version)
docker pull "$new_image"
export LLMGATEWAY_GATEWAY_IMAGE=$new_image
deployment_compose config --quiet
deployment_compose --profile migration run --rm migrate
after_version=$(migration_version)

check_public_health() {
  [[ -z "$health_url" ]] || curl --fail --silent --show-error --max-time 10 "$health_url" >/dev/null
}

rollback_application() {
  local failed_service=$1
  if [[ "$before_version" != "$after_version" ]]; then
    echo "migration version changed; refusing image-only rollback" >&2
    echo "keep the healthy instance, restore $backup_path into a new database, then switch the database URL file" >&2
    return 1
  fi
  export LLMGATEWAY_GATEWAY_IMAGE=$old_image
  deployment_compose up --detach --no-deps --force-recreate --wait "$failed_service"
  check_public_health
}

for service in gateway-a gateway-b; do
  if ! deployment_compose up --detach --no-deps --force-recreate --wait "$service"; then
    rollback_application "$service"
    exit 1
  fi
  if ! check_public_health; then
    rollback_application "$service"
    exit 1
  fi
done

temporary_environment="$environment_file.partial"
found_image=false
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" == LLMGATEWAY_GATEWAY_IMAGE=* ]]; then
    printf 'LLMGATEWAY_GATEWAY_IMAGE=%s\n' "$new_image"
    found_image=true
  else
    printf '%s\n' "$line"
  fi
done < "$environment_file" > "$temporary_environment"
$found_image || { rm -f -- "$temporary_environment"; echo "environment file has no image entry" >&2; exit 1; }
chown --reference="$environment_file" "$temporary_environment"
chmod --reference="$environment_file" "$temporary_environment"
mv -- "$temporary_environment" "$environment_file"
systemctl reload llmgateway-compose.service
echo "LLMGateway rolling upgrade completed; pre-upgrade backup: $backup_path"
