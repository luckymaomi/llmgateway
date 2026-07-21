#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 2 && $2 == --confirm-backup-repository-initialization ]] || {
  echo "usage: $0 BACKUP_ENV --confirm-backup-repository-initialization" >&2
  exit 2
}

SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"

if run_restic snapshots --json >/dev/null 2>&1; then
  echo "Restic repository is already initialized."
  exit 0
fi
run_restic init
run_restic check
echo "Encrypted Restic repository initialized and checked."
