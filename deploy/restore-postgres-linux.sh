#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 4 && $4 == --confirm-new-database-restore ]] || {
  echo "usage: $0 DEPLOYMENT_ENV POSTGRES_DUMP TARGET_DATABASE --confirm-new-database-restore" >&2
  exit 2
}
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=lib.sh
source "$SCRIPT_DIRECTORY/lib.sh"
load_llmgateway_environment "$1"
require_file_secrets
require_immutable_gateway_image
export DEPLOY_DIRECTORY=${DEPLOY_DIRECTORY:-$SCRIPT_DIRECTORY}
dump=$(realpath "$2")
target_database=$3
[[ -s $dump ]] || { echo "PostgreSQL dump is missing or empty" >&2; exit 1; }
[[ $target_database =~ ^[A-Za-z][A-Za-z0-9_]{2,62}$ && $target_database != postgres && $target_database != template* ]] || {
  echo "unsafe restore database name" >&2
  exit 1
}
: "${LLMGATEWAY_POSTGRES_USER:=llmgateway}"
existing=$(deployment_compose exec -T postgres psql --username "$LLMGATEWAY_POSTGRES_USER" --dbname postgres \
  --tuples-only --no-align --command "SELECT 1 FROM pg_database WHERE datname = '$target_database'")
[[ $existing != 1 ]] || { echo "restore target database already exists" >&2; exit 1; }
deployment_compose exec -T postgres pg_restore --list <"$dump" >/dev/null
deployment_compose exec -T postgres createdb --username "$LLMGATEWAY_POSTGRES_USER" "$target_database"
created=true
cleanup() {
  if [[ ${created:-false} == true ]]; then
    deployment_compose exec -T postgres dropdb --if-exists --username "$LLMGATEWAY_POSTGRES_USER" "$target_database" >/dev/null 2>&1 || true
  fi
}
trap cleanup ERR INT TERM
deployment_compose exec -T postgres pg_restore --exit-on-error --no-owner --no-privileges \
  --username "$LLMGATEWAY_POSTGRES_USER" --dbname "$target_database" <"$dump"
created=false
trap - ERR INT TERM
echo "PostgreSQL backup restored into new database $target_database."
