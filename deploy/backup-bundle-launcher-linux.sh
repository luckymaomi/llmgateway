#!/usr/bin/env bash
set -euo pipefail

[[ $# -ge 2 ]] || {
  echo "usage: $0 OPERATION ARGUMENT..." >&2
  exit 2
}
[[ $EUID -eq 0 ]] || { echo "backup operations require UID 0" >&2; exit 1; }

operation=$1
shift
case "$operation" in
  backup) script=backup-linux.sh ;;
  check-freshness) script=check-backup-freshness-linux.sh ;;
  check-repository) script=check-restic-repository-linux.sh ;;
  initialize) script=initialize-backup-linux.sh ;;
  install-configuration) script=install-restored-configuration-linux.sh ;;
  list) script=list-backups-linux.sh ;;
  restore-postgres) script=restore-postgres-linux.sh ;;
  restore-snapshot) script=restore-backup-linux.sh ;;
  scheduled-backup) script=run-backup-with-retries-linux.sh ;;
  *) echo "unsupported backup operation" >&2; exit 2 ;;
esac

active_link=/opt/llmgateway/backup-active
[[ -L $active_link ]] || { echo "active backup bundle link is missing or invalid" >&2; exit 1; }
bundle_directory=$(realpath -e -- "$active_link")
[[ $bundle_directory =~ ^/opt/llmgateway/backup-bundle-[A-Za-z0-9]{8}$ &&
   -d $bundle_directory && ! -L $bundle_directory &&
   $(stat -c '%u:%g:%a' -- "$bundle_directory") == 0:0:750 ]] || {
  echo "active backup bundle does not satisfy its immutable runtime contract" >&2
  exit 1
}
script_path=$bundle_directory/$script
[[ -f $script_path && ! -L $script_path && $(realpath -e -- "$script_path") == "$script_path" &&
   $(stat -c '%u:%g:%h:%a' -- "$script_path") == 0:0:1:755 ]] || {
  echo "active backup operation is missing or unsafe" >&2
  exit 1
}
exec "$script_path" "$@"
