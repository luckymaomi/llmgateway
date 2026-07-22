#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 2 && $2 == --confirm-backup-schedule ]] || {
  echo "usage: $0 /etc/llmgateway-backup/backup.env --confirm-backup-schedule" >&2
  exit 2
}
[[ $EUID -eq 0 ]] || { echo "backup schedule installation requires root" >&2; exit 1; }
SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"
[[ $1 == /etc/llmgateway-backup/backup.env && ! -L $1 ]] || {
  echo "production backup schedule requires /etc/llmgateway-backup/backup.env" >&2
  exit 1
}

if [[ ! -e /var/lib/llmgateway-backup && ! -L /var/lib/llmgateway-backup ]]; then
  install -d -o 0 -g 0 -m 0700 /var/lib/llmgateway-backup
fi
require_backup_directory "backup staging root" /var/lib/llmgateway-backup
load_backup_environment "$1"
[[ $LLMGATEWAY_BACKUP_MODE == production ]] || { echo "scheduled backups require production mode" >&2; exit 1; }
[[ $LLMGATEWAY_CONFIGURATION_DIRECTORY == /etc/llmgateway &&
   $LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE == /etc/llmgateway/deployment.env &&
   $LLMGATEWAY_BACKUP_STAGING_ROOT == /var/lib/llmgateway-backup &&
   $LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE == /var/lib/llmgateway-backup/last-success &&
   $LLMGATEWAY_RESTIC_REPOSITORY_FILE == /etc/llmgateway-backup/repository &&
   $LLMGATEWAY_RESTIC_PASSWORD_FILE == /etc/llmgateway-backup/password &&
   $LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE == /etc/llmgateway-backup/aws-credentials ]] || {
  echo "scheduled backup paths do not match the fixed systemd sandbox" >&2
  exit 1
}
if [[ -n ${LLMGATEWAY_RESTIC_AWS_CONFIG_FILE:-} &&
      $LLMGATEWAY_RESTIC_AWS_CONFIG_FILE != /etc/llmgateway-backup/aws-config ]]; then
  echo "scheduled AWS config path does not match the fixed systemd sandbox" >&2
  exit 1
fi

units=(
  llmgateway-backup.timer
  llmgateway-backup-freshness.timer
  llmgateway-backup.service
  llmgateway-backup-freshness.service
)

bundle_stage=''
link_stage=''
launcher_stage=''
unit_stages=()
maintenance_lock_held=false
cleanup() {
  local status=$? unit_stage
  trap - EXIT
  if [[ -n $link_stage && -L $link_stage ]]; then rm -f -- "$link_stage" || status=1; fi
  if [[ -n $launcher_stage && -f $launcher_stage && ! -L $launcher_stage ]]; then rm -f -- "$launcher_stage" || status=1; fi
  for unit_stage in "${unit_stages[@]}"; do
    if [[ -f $unit_stage && ! -L $unit_stage ]]; then rm -f -- "$unit_stage" || status=1; fi
  done
  if [[ -n $bundle_stage && -d $bundle_stage && ! -L $bundle_stage ]]; then rm -rf -- "$bundle_stage" || status=1; fi
  if [[ $maintenance_lock_held == true ]]; then release_llmgateway_maintenance_lock || status=1; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

acquire_llmgateway_maintenance_lock backup-installation
maintenance_lock_held=true
list_preflight_owner=backup-installation-$$-list
list_preflight_succeeded=true
if ! LLMGATEWAY_RESTIC_RUN_OWNER=$list_preflight_owner timeout --signal=TERM --kill-after=30s 5m \
    "$SCRIPT_DIRECTORY/list-backups-linux.sh" "$1" >/dev/null; then
  list_preflight_succeeded=false
fi
cleanup_restic_execution "$list_preflight_owner" || {
  echo "could not prove Restic list preflight container cleanup" >&2
  exit 1
}
if [[ $list_preflight_succeeded != true ]]; then
  echo "Restic repository must be initialized separately before schedule installation" >&2
  exit 1
fi
check_preflight_owner=backup-installation-$$-check
check_preflight_succeeded=true
if ! LLMGATEWAY_RESTIC_RUN_OWNER=$check_preflight_owner timeout --signal=TERM --kill-after=30s 20m \
    "$SCRIPT_DIRECTORY/check-restic-repository-linux.sh" "$1"; then
  check_preflight_succeeded=false
fi
cleanup_restic_execution "$check_preflight_owner" || {
  echo "could not prove Restic check preflight container cleanup" >&2
  exit 1
}
if [[ $check_preflight_succeeded != true ]]; then
  echo "Restic repository preflight check failed or timed out" >&2
  exit 1
fi

if [[ ! -e /opt/llmgateway && ! -L /opt/llmgateway ]]; then
  install -d -o 0 -g 0 -m 0750 /opt/llmgateway
fi
[[ -d /opt/llmgateway && ! -L /opt/llmgateway && $(stat -c '%u:%g:%a' /opt/llmgateway) == 0:0:750 ]] || {
  echo "/opt/llmgateway must be a root-owned 0750 directory" >&2
  exit 1
}
require_root_owned_path_ancestors "backup bundle directory" /opt/llmgateway/backup-active
require_root_owned_path_ancestors "systemd unit target" /etc/systemd/system/llmgateway-backup.service
active_bundle_before_installation=''
if [[ -e /opt/llmgateway/backup-active || -L /opt/llmgateway/backup-active ]]; then
  [[ -L /opt/llmgateway/backup-active ]] || { echo "active backup bundle must be a symbolic link" >&2; exit 1; }
  active_bundle_before_installation=$(realpath -e -- /opt/llmgateway/backup-active)
  [[ $active_bundle_before_installation =~ ^/opt/llmgateway/backup-bundle-[A-Za-z0-9]{8}$ &&
     -d $active_bundle_before_installation && ! -L $active_bundle_before_installation &&
     $(stat -c '%u:%g:%a' -- "$active_bundle_before_installation") == 0:0:750 ]] || {
    echo "installed backup bundle does not satisfy its runtime contract" >&2
    exit 1
  }
fi
remove_stale_private_directories /opt/llmgateway .backup-bundle. '700|750'
bundle_stage=$(mktemp -d /opt/llmgateway/.backup-bundle.XXXXXXXX)
bundle_suffix=${bundle_stage##*.}
bundle_directory=/opt/llmgateway/backup-bundle-$bundle_suffix

for file in lib.sh backup-lib.sh compose.production.yaml Caddyfile; do
  install -m 0644 "$SCRIPT_DIRECTORY/$file" "$bundle_stage/$file"
done
for file in initialize-backup-linux.sh backup-linux.sh run-backup-with-retries-linux.sh \
  check-backup-freshness-linux.sh check-restic-repository-linux.sh list-backups-linux.sh restore-backup-linux.sh \
  restore-postgres-linux.sh install-restored-configuration-linux.sh install-backup-linux.sh; do
  install -m 0755 "$SCRIPT_DIRECTORY/$file" "$bundle_stage/$file"
done
for file in llmgateway-backup.service llmgateway-backup.timer \
  llmgateway-backup-freshness.service llmgateway-backup-freshness.timer; do
  install -m 0644 "$SCRIPT_DIRECTORY/$file" "$bundle_stage/$file"
done
chown -R 0:0 "$bundle_stage"
chmod 0750 "$bundle_stage"
bash -n "$bundle_stage"/*.sh
(
  load_llmgateway_environment /etc/llmgateway/deployment.env
  require_file_secrets
  require_immutable_gateway_image
  export DEPLOY_DIRECTORY=$bundle_stage
  deployment_compose config --quiet
)

mv -T -- "$bundle_stage" "$bundle_directory"
bundle_stage=''
link_stage=/opt/llmgateway/.backup-active.$bundle_suffix
ln -s "$(basename -- "$bundle_directory")" "$link_stage"
mv -Tf -- "$link_stage" /opt/llmgateway/backup-active
link_stage=''

launcher_stage=/opt/llmgateway/.backup-bundle-launcher.$bundle_suffix
install -o 0 -g 0 -m 0755 "$SCRIPT_DIRECTORY/backup-bundle-launcher-linux.sh" "$launcher_stage"
mv -Tf -- "$launcher_stage" /opt/llmgateway/backup-bundle-launcher-linux.sh
launcher_stage=''

for file in llmgateway-backup.service llmgateway-backup.timer \
  llmgateway-backup-freshness.service llmgateway-backup-freshness.timer; do
  [[ ! -d /etc/systemd/system/$file ]] || { echo "systemd unit target must not be a directory" >&2; exit 1; }
  unit_stage="/etc/systemd/system/.$file.$bundle_suffix"
  [[ ! -e $unit_stage && ! -L $unit_stage ]] || { echo "systemd unit staging path already exists" >&2; exit 1; }
  install -o 0 -g 0 -m 0644 "$bundle_directory/$file" "$unit_stage"
  unit_stages+=("$unit_stage")
done
for file in llmgateway-backup.service llmgateway-backup.timer \
  llmgateway-backup-freshness.service llmgateway-backup-freshness.timer; do
  unit_stage="/etc/systemd/system/.$file.$bundle_suffix"
  mv -Tf -- "$unit_stage" "/etc/systemd/system/$file"
done
unit_stages=()
systemctl daemon-reload
systemctl reset-failed "${units[@]}" >/dev/null 2>&1 || true
systemctl enable --now llmgateway-backup.timer llmgateway-backup-freshness.timer
release_llmgateway_maintenance_lock
maintenance_lock_held=false
trap - EXIT INT TERM

initial_backup_succeeded=true
if ! systemctl restart llmgateway-backup.service; then
  initial_backup_succeeded=false
elif [[ $(systemctl show --property=Result --value llmgateway-backup.service) != success ]]; then
  initial_backup_succeeded=false
fi
systemctl is-active --quiet llmgateway-backup.timer
systemctl is-active --quiet llmgateway-backup-freshness.timer
[[ $initial_backup_succeeded == true ]] || {
  if [[ -n $active_bundle_before_installation ]]; then
    link_stage=/opt/llmgateway/.backup-active.rollback.$bundle_suffix
    ln -s "$(basename -- "$active_bundle_before_installation")" "$link_stage"
    mv -Tf -- "$link_stage" /opt/llmgateway/backup-active
    link_stage=''
    systemctl daemon-reload
    systemctl reset-failed llmgateway-backup.service >/dev/null 2>&1 || true
    systemctl restart llmgateway-backup.timer llmgateway-backup-freshness.timer
    echo "initial scheduled backup failed; the active script bundle was rolled back and timers remain active" >&2
  else
    echo "initial scheduled backup failed; no earlier bundle exists and timers remain active for retry and freshness alerts" >&2
  fi
  exit 1
}
echo "LLMGateway scheduled S3 backup and independent freshness monitoring installed."
