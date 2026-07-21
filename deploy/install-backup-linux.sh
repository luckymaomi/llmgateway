#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 2 && $2 == --confirm-backup-schedule ]] || {
  echo "usage: $0 BACKUP_ENV --confirm-backup-schedule" >&2
  exit 2
}
[[ $EUID -eq 0 ]] || { echo "backup schedule installation requires root" >&2; exit 1; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
backup_environment=$(realpath "$1")
install -d -m 0750 /opt/llmgateway/deploy
install -m 0644 "$SCRIPT_DIRECTORY/backup-lib.sh" /opt/llmgateway/deploy/backup-lib.sh
install -m 0755 "$SCRIPT_DIRECTORY/initialize-backup-linux.sh" /opt/llmgateway/deploy/initialize-backup-linux.sh
install -m 0755 "$SCRIPT_DIRECTORY/backup-linux.sh" /opt/llmgateway/deploy/backup-linux.sh
install -m 0755 "$SCRIPT_DIRECTORY/restore-backup-linux.sh" /opt/llmgateway/deploy/restore-backup-linux.sh
install -m 0755 "$SCRIPT_DIRECTORY/restore-postgres-linux.sh" /opt/llmgateway/deploy/restore-postgres-linux.sh
install -d -m 0700 /var/lib/llmgateway-backup
"$SCRIPT_DIRECTORY/initialize-backup-linux.sh" "$backup_environment" --confirm-backup-repository-initialization
install -m 0644 "$SCRIPT_DIRECTORY/llmgateway-backup.service" /etc/systemd/system/llmgateway-backup.service
install -m 0644 "$SCRIPT_DIRECTORY/llmgateway-backup.timer" /etc/systemd/system/llmgateway-backup.timer
sed -i "s|@@BACKUP_ENVIRONMENT@@|$backup_environment|g" /etc/systemd/system/llmgateway-backup.service
systemctl daemon-reload
systemctl enable --now llmgateway-backup.timer
systemctl start llmgateway-backup.service
systemctl --no-pager --full status llmgateway-backup.service
