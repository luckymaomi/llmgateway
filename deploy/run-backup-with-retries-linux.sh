#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { echo "usage: $0 BACKUP_ENV" >&2; exit 2; }
[[ $EUID -eq 0 ]] || { echo "scheduled backup requires UID 0" >&2; exit 1; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"

trap 'exit 130' INT
trap 'exit 143' TERM
for attempt in 1 2; do
  restic_run_owner=scheduled-$$-$attempt
  if LLMGATEWAY_RESTIC_RUN_OWNER=$restic_run_owner timeout --signal=TERM --kill-after=30s 35m \
      "$SCRIPT_DIRECTORY/backup-linux.sh" "$1"; then
    cleanup_restic_execution "$restic_run_owner"
    exit 0
  else
    status=$?
  fi
  if ! cleanup_restic_execution "$restic_run_owner"; then
    logger --priority daemon.alert --tag llmgateway-backup \
      "LLMGateway could not clean the timed-out Restic container; retry is blocked" 2>/dev/null || true
    exit 1
  fi
  logger --priority daemon.alert --tag llmgateway-backup \
    "LLMGateway scheduled backup attempt $attempt of 2 failed" 2>/dev/null || true
  if [[ $attempt -eq 2 ]]; then
    exit "$status"
  fi
  sleep 10m
done
