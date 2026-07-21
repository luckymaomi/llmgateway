#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 3 && $3 == --confirm-disaster-restore ]] || {
  echo "usage: $0 BACKUP_ENV EMPTY_RESTORE_DIRECTORY --confirm-disaster-restore" >&2
  exit 2
}
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"

target_parent=$(realpath "$(dirname -- "$2")")
target="$target_parent/$(basename -- "$2")"
[[ ! -L $target ]] || { echo "restore directory must not be a symbolic link" >&2; exit 1; }
if [[ -e $target ]]; then
  target=$(realpath "$target")
fi
[[ $target != / && $target != "$HOME" ]] || { echo "unsafe restore directory" >&2; exit 1; }
if [[ -e $target && -n $(find "$target" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  echo "restore directory must be empty" >&2
  exit 1
fi
mkdir -p "$target"
chmod 0700 "$target"

started=$(date +%s)
RESTIC_DATA_MOUNT_SOURCE=$target
RESTIC_DATA_MOUNT_TARGET=/restore
RESTIC_DATA_MOUNT_READONLY=
RESTIC_ALLOW_CHOWN=true
run_restic restore latest \
  --host llmgateway-production --tag llmgateway-production --target /restore
unset RESTIC_DATA_MOUNT_SOURCE RESTIC_DATA_MOUNT_TARGET RESTIC_DATA_MOUNT_READONLY RESTIC_ALLOW_CHOWN
dump="$target/backup/postgres.dump"
[[ -s $dump && -d $target/backup/configuration ]] || { echo "restored snapshot is incomplete" >&2; exit 1; }
(cd "$target/backup" && sha256sum -c postgres.dump.sha256 >/dev/null)
: "${LLMGATEWAY_RUNTIME_SECRET_GID:=65532}"
[[ $LLMGATEWAY_RUNTIME_SECRET_GID =~ ^[1-9][0-9]{1,9}$ ]] || { echo "runtime secret GID is invalid" >&2; exit 1; }
chmod 0700 "$target" "$target/backup"
chmod 0600 "$dump" "$target/backup/postgres.dump.sha256"
chown -R "0:$LLMGATEWAY_RUNTIME_SECRET_GID" "$target/backup/configuration"
find "$target/backup/configuration" -type d -exec chmod 0750 {} +
find "$target/backup/configuration" -type f -exec chmod 0640 {} +
elapsed=$(( $(date +%s) - started ))
echo "Encrypted snapshot restored and verified into an empty directory in ${elapsed}s."
