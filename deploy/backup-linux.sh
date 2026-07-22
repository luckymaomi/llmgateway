#!/usr/bin/env bash
set -euo pipefail
umask 0077

[[ $# -eq 1 ]] || { echo "usage: $0 BACKUP_ENV" >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo "backup requires UID 0" >&2; exit 1; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"
check_backup_freshness >/dev/null || true

configuration_directory=$LLMGATEWAY_CONFIGURATION_DIRECTORY
deployment_environment_file=$LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE
staging_root=$LLMGATEWAY_BACKUP_STAGING_ROOT
success_marker=$LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE
staging=''
marker_temporary=''
maintenance_lock_held=false

cleanup() {
  local status=$?
  trap - EXIT INT TERM
  set +e
  unset RESTIC_DATA_MOUNT_SOURCE RESTIC_DATA_MOUNT_TARGET RESTIC_DATA_MOUNT_READONLY
  if [[ -n $marker_temporary && -e $marker_temporary ]]; then
    rm -f -- "$marker_temporary" || status=1
  fi
  if [[ -n $staging ]]; then
    if [[ $staging == "$staging_root"/backup.* && -d $staging && ! -L $staging ]]; then
      rm -rf -- "$staging" || status=1
    else
      echo "refusing to clean an unexpected backup staging path" >&2
      status=1
    fi
  fi
  if [[ $maintenance_lock_held == true ]]; then
    release_llmgateway_maintenance_lock || status=1
    maintenance_lock_held=false
  fi
  if (( status != 0 )); then
    logger --priority daemon.alert --tag llmgateway-backup "LLMGateway encrypted backup failed" 2>/dev/null || true
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

acquire_llmgateway_maintenance_lock backup
maintenance_lock_held=true
remove_stale_private_directories "$staging_root" backup.
staging=$(mktemp -d "$staging_root/backup.XXXXXXXX")
chmod 0700 "$staging"

verify_runtime_configuration_tree "$configuration_directory"
while IFS= read -r line || [[ -n $line ]]; do
  line=${line%$'\r'}
  [[ -z $line || $line == \#* ]] && continue
  key=${line%%=*}
  case "$key" in
    LLMGATEWAY_BACKUP_*|LLMGATEWAY_RESTIC_*|LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE|LLMGATEWAY_CONFIGURATION_DIRECTORY)
      backup_error "deployment.env must not redefine backup control settings"
      ;;
  esac
done < "$deployment_environment_file"

mkdir -m 0700 "$staging/configuration"
cp -a -- "$configuration_directory/." "$staging/configuration/"
chown -R 0:0 "$staging/configuration"
find "$staging/configuration" -type d -exec chmod 0700 {} +
find "$staging/configuration" -type f -exec chmod 0400 {} +
verify_backup_configuration_tree "$staging/configuration"
load_llmgateway_environment "$staging/configuration/deployment.env"
require_file_secrets
require_immutable_gateway_image

require_configuration_bindings "$configuration_directory"

export DEPLOY_DIRECTORY=${DEPLOY_DIRECTORY:-$SCRIPT_DIRECTORY}
: "${LLMGATEWAY_POSTGRES_USER:=llmgateway}"
: "${LLMGATEWAY_POSTGRES_DB:=llmgateway}"
migration_version=$(deployment_compose exec -T postgres psql \
  --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" \
  --tuples-only --no-align \
  --command 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied')
[[ $migration_version =~ ^[0-9]+$ ]] || backup_error "could not determine an applied migration version"
database_size_bytes=$(deployment_compose exec -T postgres psql \
  --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" \
  --tuples-only --no-align \
  --command 'SELECT pg_database_size(current_database())')
[[ $database_size_bytes =~ ^[0-9]+$ ]] || backup_error "could not determine the PostgreSQL database size"
(( database_size_bytes <= 7000000000000000000 )) || backup_error "PostgreSQL database size exceeds the supported backup bound"
available_kib=$(df -Pk "$staging" | awk 'NR == 2 { print $4 }')
[[ $available_kib =~ ^[0-9]+$ ]] || backup_error "could not determine backup staging free space"
available_bytes=$(( available_kib * 1024 ))
capacity_reserve_bytes=$(( database_size_bytes / 5 ))
(( capacity_reserve_bytes >= 1073741824 )) || capacity_reserve_bytes=1073741824
required_capacity_bytes=$(( database_size_bytes + capacity_reserve_bytes ))
(( available_bytes >= required_capacity_bytes )) || {
  backup_error "backup staging has insufficient free space for the database dump and safety reserve"
}
recovery_point=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
deployment_compose exec -T postgres pg_dump \
  --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" \
  --format custom --compress 9 > "$staging/postgres.dump"
[[ -s $staging/postgres.dump ]] || backup_error "PostgreSQL dump is empty"
deployment_compose exec -T postgres pg_restore --list < "$staging/postgres.dump" >/dev/null

write_configuration_checksum "$staging/configuration" "$staging/configuration.sha256"
postgres_digest=$(sha256sum -- "$staging/postgres.dump" | awk '{print $1}')
printf '%s  postgres.dump\n' "$postgres_digest" > "$staging/postgres.dump.sha256"
configuration_digest=$(sha256sum -- "$staging/configuration.sha256" | awk '{print $1}')
gateway_digest=${LLMGATEWAY_GATEWAY_IMAGE##*@}
printf '%s\n' \
  'format=llmgateway-backup' \
  "recovery_point_utc=$recovery_point" \
  "migration_version=$migration_version" \
  "gateway_image=$LLMGATEWAY_GATEWAY_IMAGE" \
  "gateway_image_digest=$gateway_digest" \
  "configuration_sha256=sha256:$configuration_digest" \
  "postgres_dump_sha256=sha256:$postgres_digest" \
  > "$staging/backup-manifest"
chmod 0600 "$staging/postgres.dump" "$staging/postgres.dump.sha256" \
  "$staging/configuration.sha256" "$staging/backup-manifest"
verify_backup_payload "$staging"

RESTIC_DATA_MOUNT_SOURCE=$staging
RESTIC_DATA_MOUNT_TARGET=/backup
RESTIC_DATA_MOUNT_READONLY=true
run_restic backup /backup --host llmgateway-production --tag llmgateway-production
unset RESTIC_DATA_MOUNT_SOURCE RESTIC_DATA_MOUNT_TARGET RESTIC_DATA_MOUNT_READONLY

run_restic forget --host llmgateway-production --tag llmgateway-production \
  --keep-daily 7 --keep-weekly 5 --keep-monthly 12 --prune
run_restic check --read-data-subset "$LLMGATEWAY_RESTIC_CHECK_SUBSET"

completed_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
marker_temporary=$(mktemp "$staging_root/.last-success.XXXXXXXX")
printf '%s\n' \
  'format=llmgateway-backup-success' \
  "recovery_point_utc=$recovery_point" \
  "completed_at_utc=$completed_at" \
  > "$marker_temporary"
chown 0:0 "$marker_temporary"
chmod 0600 "$marker_temporary"
mv -Tf -- "$marker_temporary" "$success_marker"
marker_temporary=''
if [[ $LLMGATEWAY_BACKUP_MODE == production ]]; then
  echo "Encrypted remote S3 database and configuration backup completed."
else
  echo "Encrypted acceptance database and configuration backup completed."
fi
