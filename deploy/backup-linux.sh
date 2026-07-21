#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { echo "usage: $0 BACKUP_ENV" >&2; exit 2; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"

: "${LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE:?set the production deployment environment file}"
load_llmgateway_environment "$LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE"
: "${LLMGATEWAY_CONFIGURATION_DIRECTORY:?set the configuration directory to back up}"
: "${LLMGATEWAY_BACKUP_STAGING_ROOT:?set the backup staging root}"
[[ -d $LLMGATEWAY_CONFIGURATION_DIRECTORY ]] || { echo "configuration directory does not exist" >&2; exit 1; }
unsupported_entry=$(find "$LLMGATEWAY_CONFIGURATION_DIRECTORY" -mindepth 1 ! -type f ! -type d -print -quit)
if [[ -n $unsupported_entry ]]; then
  echo "configuration directory must contain only regular files and directories" >&2
  exit 1
fi
mkdir -p "$LLMGATEWAY_BACKUP_STAGING_ROOT"
chmod 0700 "$LLMGATEWAY_BACKUP_STAGING_ROOT"
staging=$(mktemp -d "$LLMGATEWAY_BACKUP_STAGING_ROOT/backup.XXXXXXXX")
cleanup() { rm -rf -- "$staging"; }
trap cleanup EXIT INT TERM

mkdir -p "$staging/configuration"
cp -a -- "$LLMGATEWAY_CONFIGURATION_DIRECTORY/." "$staging/configuration/"
chmod -R go-rwx "$staging"

export DEPLOY_DIRECTORY=${DEPLOY_DIRECTORY:-$SCRIPT_DIRECTORY}
: "${LLMGATEWAY_POSTGRES_USER:=llmgateway}"
: "${LLMGATEWAY_POSTGRES_DB:=llmgateway}"
deployment_compose exec -T postgres pg_dump --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" --format custom --compress 9 >"$staging/postgres.dump"
[[ -s $staging/postgres.dump ]] || { echo "PostgreSQL dump is empty" >&2; exit 1; }
deployment_compose exec -T postgres pg_restore --list <"$staging/postgres.dump" >/dev/null
(cd "$staging" && sha256sum postgres.dump >postgres.dump.sha256)

RESTIC_DATA_MOUNT_SOURCE=$staging
RESTIC_DATA_MOUNT_TARGET=/backup
RESTIC_DATA_MOUNT_READONLY=true
run_restic backup /backup \
  --host llmgateway-production --tag llmgateway-production
unset RESTIC_DATA_MOUNT_SOURCE RESTIC_DATA_MOUNT_TARGET RESTIC_DATA_MOUNT_READONLY
run_restic forget --host llmgateway-production --tag llmgateway-production \
  --keep-daily 7 --keep-weekly 5 --keep-monthly 12 --prune
run_restic check --read-data-subset "${LLMGATEWAY_RESTIC_CHECK_SUBSET:-5%}"
echo "Encrypted database and configuration backup completed."
