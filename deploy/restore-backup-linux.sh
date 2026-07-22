#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 4 && $4 == --confirm-disaster-restore ]] || {
  echo "usage: $0 BACKUP_ENV SNAPSHOT_ID EMPTY_RESTORE_DIRECTORY --confirm-disaster-restore" >&2
  exit 2
}
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"

snapshot_id=$2
[[ $snapshot_id =~ ^[a-f0-9]{64}$ ]] || {
  echo "snapshot ID must contain exactly 64 lowercase hexadecimal characters" >&2
  exit 1
}

target=$(configured_path "restore directory" "$3")
target_parent=$(dirname -- "$target")
[[ -d $target_parent && ! -L $target_parent ]] || { echo "restore directory parent is invalid" >&2; exit 1; }
[[ $(realpath -e -- "$target_parent") == "$target_parent" ]] || { echo "restore directory parent is unsafe" >&2; exit 1; }
[[ $(stat -c '%u' -- "$target_parent") == 0 ]] || { echo "restore directory parent must be owned by UID 0" >&2; exit 1; }
parent_mode=$(stat -c '%a' -- "$target_parent")
(( (8#$parent_mode & 8#7022) == 0 )) || { echo "restore directory parent has unsafe permissions" >&2; exit 1; }
require_root_owned_path_ancestors "restore directory" "$target"

protected_paths=(
  "$(realpath -e -- "$1")"
  "$LLMGATEWAY_RESTIC_REPOSITORY_FILE"
  "$LLMGATEWAY_RESTIC_PASSWORD_FILE"
  "$LLMGATEWAY_CONFIGURATION_DIRECTORY"
  "$LLMGATEWAY_BACKUP_STAGING_ROOT"
)
for variable in LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE LLMGATEWAY_RESTIC_AWS_CONFIG_FILE LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY; do
  if [[ -n ${!variable:-} ]]; then protected_paths+=("${!variable}"); fi
done
for protected_path in "${protected_paths[@]}"; do
  paths_overlap "$target" "$protected_path" && {
    echo "restore directory overlaps backup control or source storage" >&2
    exit 1
  }
done

if [[ -e $target || -L $target ]]; then
  [[ -d $target && ! -L $target && $(realpath -e -- "$target") == "$target" ]] || {
    echo "restore directory must be a non-symbolic-link directory" >&2
    exit 1
  }
  [[ -z $(find "$target" -mindepth 1 -maxdepth 1 -print -quit) ]] || {
    echo "restore directory must be empty" >&2
    exit 1
  }
  [[ $(stat -c '%u:%g:%a' -- "$target") == 0:0:700 ]] || {
    echo "empty restore directory must be owned by 0:0 with mode 0700" >&2
    exit 1
  }
fi

restore_stage=''
lock_acquired=false
cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n $restore_stage && -d $restore_stage && ! -L $restore_stage ]]; then rm -rf -- "$restore_stage" || status=1; fi
  if [[ $lock_acquired == true ]] && ! release_llmgateway_maintenance_lock; then status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

acquire_llmgateway_maintenance_lock backup-restore
lock_acquired=true
remove_stale_private_directories "$target_parent" .llmgateway-restore.
started=$(date +%s)
snapshot_selection=$(run_restic snapshots --json --host llmgateway-production --tag llmgateway-production "$snapshot_id")
if [[ $snapshot_selection =~ ^[[:space:]]*\[[[:space:]]*\][[:space:]]*$ ]]; then
  echo "snapshot ID does not identify a production LLMGateway snapshot" >&2
  exit 1
fi
unset snapshot_selection

restore_stage=$(mktemp -d "$target_parent/.llmgateway-restore.XXXXXXXX")
chmod 0700 "$restore_stage"
RESTIC_DATA_MOUNT_SOURCE=$restore_stage
RESTIC_DATA_MOUNT_TARGET=/restore
RESTIC_DATA_MOUNT_READONLY=
RESTIC_ALLOW_CHOWN=true
run_restic restore "$snapshot_id" --target /restore
unset RESTIC_DATA_MOUNT_SOURCE RESTIC_DATA_MOUNT_TARGET RESTIC_DATA_MOUNT_READONLY RESTIC_ALLOW_CHOWN
[[ -d $restore_stage/backup && ! -L $restore_stage/backup ]] || { echo "restored snapshot has an invalid root" >&2; exit 1; }
unexpected_root_entry=$(find "$restore_stage" -mindepth 1 -maxdepth 1 ! -path "$restore_stage/backup" -print -quit)
[[ -z $unexpected_root_entry ]] || { echo "restored snapshot contains an unexpected root entry" >&2; exit 1; }
verify_backup_payload "$restore_stage/backup"

chown 0:0 "$restore_stage" "$restore_stage/backup"
chmod 0700 "$restore_stage" "$restore_stage/backup"
chown 0:0 "$restore_stage/backup/postgres.dump" "$restore_stage/backup/postgres.dump.sha256" \
  "$restore_stage/backup/configuration.sha256" "$restore_stage/backup/backup-manifest"
chmod 0400 "$restore_stage/backup/postgres.dump" "$restore_stage/backup/postgres.dump.sha256" \
  "$restore_stage/backup/configuration.sha256" "$restore_stage/backup/backup-manifest"
verify_backup_payload "$restore_stage/backup"

if [[ -e $target ]]; then rmdir -- "$target"; fi
[[ ! -e $target && ! -L $target ]] || { echo "restore directory appeared during recovery" >&2; exit 1; }
mv -T -- "$restore_stage" "$target"
restore_stage=''
release_llmgateway_maintenance_lock
lock_acquired=false
trap - EXIT INT TERM
elapsed=$(( $(date +%s) - started ))
echo "Encrypted snapshot $snapshot_id restored and verified into an empty directory in ${elapsed}s."
