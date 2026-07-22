#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || {
  echo "usage: $0 BACKUP_ENV" >&2
  exit 2
}

SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
load_backup_environment "$1"

run_restic snapshots --json --host llmgateway-production --tag llmgateway-production
