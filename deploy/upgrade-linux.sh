#!/usr/bin/env bash
set -euo pipefail
umask 0077

usage() {
  echo "usage: upgrade-linux.sh NEW_IMAGE@sha256:DIGEST /absolute/backup.dump [https://health-url]" >&2
  exit 2
}

[[ $# -ge 2 && $# -le 3 ]] || usage
[[ $(id -u) -eq 0 ]] || { echo "upgrade requires root" >&2; exit 1; }
new_image=$1
backup_path=$2
health_url=${3:-}
[[ "$backup_path" == /* && "$backup_path" != / && "$backup_path" != *//* &&
  "$backup_path" != */./* && "$backup_path" != */../* && "$backup_path" != */. &&
  "$backup_path" != */.. && "$backup_path" != */ && "$backup_path" != *$'\n'* &&
  "$backup_path" != *$'\r'* ]] || {
  echo "backup path must be a normalized, non-root absolute path" >&2
  exit 1
}

DEPLOY_DIRECTORY=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$DEPLOY_DIRECTORY/lib.sh"
environment_file=/etc/llmgateway/deployment.env
load_llmgateway_environment "$environment_file"
require_file_secrets
require_immutable_gateway_image
old_image=$LLMGATEWAY_GATEWAY_IMAGE
[[ "$new_image" =~ @sha256:[a-f0-9]{64}$ ]] || { echo "new image must be an immutable sha256 reference" >&2; exit 1; }

require_safe_backup_ancestors() {
  local path=$1 cursor mode
  cursor=$(dirname -- "$path")
  while :; do
    [[ -d $cursor && ! -L $cursor && $(stat -c '%u' -- "$cursor") == 0 ]] || {
      echo "backup path ancestors must be root-owned directories without symbolic links" >&2
      return 1
    }
    mode=$(stat -c '%a' -- "$cursor")
    (( (8#$mode & 8#0022) == 0 )) || {
      echo "backup path ancestors must not be writable by group or world" >&2
      return 1
    }
    [[ $cursor == / ]] && break
    cursor=$(dirname -- "$cursor")
  done
}

require_safe_backup_directory() {
  local path=$1 canonical mode
  [[ -d $path && ! -L $path ]] || {
    echo "backup directory must be a non-symbolic-link directory" >&2
    return 1
  }
  canonical=$(realpath -e -- "$path") || return 1
  [[ $canonical == "$path" ]] || {
    echo "backup directory path must not contain symbolic links" >&2
    return 1
  }
  require_safe_backup_ancestors "$path/backup.dump" || return 1
  [[ $(stat -c '%u:%g' -- "$path") == 0:0 ]] || {
    echo "backup directory must be owned by root:root" >&2
    return 1
  }
  mode=$(stat -c '%a' -- "$path")
  [[ $mode == 700 ]] || {
    echo "backup directory must have mode 0700" >&2
    return 1
  }
}

require_safe_backup_file() {
  local label=$1 path=$2 allowed_modes=$3 expected_device=$4 canonical mode
  [[ -f $path && ! -L $path ]] || {
    echo "$label must be a non-symbolic-link regular file" >&2
    return 1
  }
  canonical=$(realpath -e -- "$path") || return 1
  [[ $canonical == "$path" ]] || {
    echo "$label path must not contain symbolic links" >&2
    return 1
  }
  [[ $(stat -c '%u:%g:%h:%d' -- "$path") == "0:0:1:$expected_device" ]] || {
    echo "$label must be root-owned, singly linked, and on the backup directory filesystem" >&2
    return 1
  }
  mode=$(stat -c '%a' -- "$path")
  [[ $mode =~ ^($allowed_modes)$ ]] || {
    echo "$label has an unsafe mode" >&2
    return 1
  }
}

remove_stale_upgrade_backup() {
  local path=$1 directory_device=$2
  if [[ ! -e $path && ! -L $path ]]; then
    return 0
  fi
  # A kill between sealing and rename can leave the reserved partial name at 0400.
  require_safe_backup_file "stale upgrade backup" "$path" '600|400' "$directory_device" || {
    echo "refusing to remove unsafe stale upgrade backup: $path" >&2
    return 1
  }
  rm -- "$path"
  [[ ! -e $path && ! -L $path ]] || {
    echo "could not remove stale upgrade backup: $path" >&2
    return 1
  }
  echo "removed verified stale upgrade backup: $path" >&2
}

maintenance_lock_held=false
staged_backup=''
staged_backup_identity=''
backup_output_fd=''
backup_input_fd=''

cleanup_upgrade() {
  local status=$? current_identity='' directory_device=''
  trap - EXIT
  if [[ $backup_input_fd =~ ^[0-9]+$ ]]; then exec {backup_input_fd}<&- || status=1; fi
  if [[ $backup_output_fd =~ ^[0-9]+$ ]]; then exec {backup_output_fd}>&- || status=1; fi
  if [[ -n $staged_backup_identity && ( -e $staged_backup || -L $staged_backup ) ]]; then
    current_identity=$(stat -Lc '%d:%i' -- "$staged_backup" 2>/dev/null || true)
    directory_device=$(stat -c '%d' -- "$(dirname -- "$staged_backup")" 2>/dev/null || true)
    if [[ $current_identity == "$staged_backup_identity" ]] &&
      require_safe_backup_file "incomplete upgrade backup" "$staged_backup" '600|400' "$directory_device"; then
      rm -- "$staged_backup" || status=1
    else
      echo "refusing to remove an upgrade backup whose identity or security contract changed" >&2
      status=1
    fi
  fi
  if [[ $maintenance_lock_held == true ]]; then
    release_llmgateway_maintenance_lock || status=1
  fi
  exit "$status"
}

trap cleanup_upgrade EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

acquire_llmgateway_maintenance_lock upgrade
maintenance_lock_held=true

backup_directory=$(dirname -- "$backup_path")
if [[ ! -e $backup_directory && ! -L $backup_directory ]]; then
  require_safe_backup_ancestors "$backup_directory" || exit 1
  install --directory --owner=0 --group=0 --mode=0700 -- "$backup_directory"
fi
require_safe_backup_directory "$backup_directory"
[[ ! -e $backup_path && ! -L $backup_path ]] || { echo "backup path must be absent" >&2; exit 1; }
backup_directory_device=$(stat -c '%d' -- "$backup_directory")
staged_backup="$backup_path.partial"
remove_stale_upgrade_backup "$staged_backup" "$backup_directory_device"

database_bytes=$(deployment_compose exec -T postgres psql --username "$LLMGATEWAY_POSTGRES_USER" --dbname "$LLMGATEWAY_POSTGRES_DB" --tuples-only --no-align --command 'SELECT pg_database_size(current_database())')
available_kib=$(df -Pk "$backup_directory" | awk 'NR==2 {print $4}')
[[ "$database_bytes" =~ ^[0-9]+$ && "$available_kib" =~ ^[0-9]+$ ]] || { echo "could not measure backup capacity" >&2; exit 1; }
(( available_kib * 1024 >= database_bytes * 2 )) || { echo "backup target has less than twice the database size available" >&2; exit 1; }

set -o noclobber
if ! exec {backup_output_fd}> "$staged_backup"; then
  set +o noclobber
  echo "could not exclusively create the staged upgrade backup" >&2
  exit 1
fi
set +o noclobber
chmod 0600 "$staged_backup"
staged_backup_identity=$(stat -Lc '%d:%i' -- "/proc/$$/fd/$backup_output_fd")
[[ $(stat -Lc '%d:%i' -- "$staged_backup") == "$staged_backup_identity" ]] || {
  echo "staged upgrade backup changed while it was opened" >&2
  exit 1
}
require_safe_backup_file "staged upgrade backup" "$staged_backup" 600 "$backup_directory_device"

deployment_compose exec -T postgres pg_dump \
  --username "$LLMGATEWAY_POSTGRES_USER" \
  --dbname "$LLMGATEWAY_POSTGRES_DB" \
  --format custom --compress 9 >&"$backup_output_fd"
exec {backup_output_fd}>&-
backup_output_fd=''
[[ -s $staged_backup ]] || { echo "staged upgrade backup is empty" >&2; exit 1; }
sync -f "$staged_backup"

exec {backup_input_fd}< "$staged_backup"
[[ $(stat -Lc '%d:%i' -- "/proc/$$/fd/$backup_input_fd") == "$staged_backup_identity" &&
  $(stat -Lc '%d:%i' -- "$staged_backup") == "$staged_backup_identity" ]] || {
  echo "staged upgrade backup changed before validation" >&2
  exit 1
}
require_safe_backup_file "staged upgrade backup" "$staged_backup" 600 "$backup_directory_device"
deployment_compose exec -T postgres pg_restore --list <&"$backup_input_fd" >/dev/null
exec {backup_input_fd}<&-
backup_input_fd=''

chmod 0400 "$staged_backup"
require_safe_backup_file "sealed upgrade backup" "$staged_backup" 400 "$backup_directory_device"
mv --no-clobber --no-target-directory -- "$staged_backup" "$backup_path"
[[ ! -e $staged_backup && ! -L $staged_backup && -e $backup_path ]] || {
  echo "backup path appeared before the staged backup could be published" >&2
  exit 1
}
[[ $(stat -Lc '%d:%i' -- "$backup_path") == "$staged_backup_identity" ]] || {
  echo "published upgrade backup identity changed" >&2
  exit 1
}
require_safe_backup_file "published upgrade backup" "$backup_path" 400 "$backup_directory_device"
sync -f "$backup_path"
staged_backup_identity=''

migration_version() {
  deployment_compose exec -T postgres psql \
    --username "$LLMGATEWAY_POSTGRES_USER" \
    --dbname "$LLMGATEWAY_POSTGRES_DB" \
    --tuples-only --no-align \
    --command 'SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied'
}

before_version=$(migration_version)
docker pull "$new_image"
export LLMGATEWAY_GATEWAY_IMAGE=$new_image
deployment_compose config --quiet
deployment_compose --profile migration run --rm migrate
after_version=$(migration_version)

check_public_health() {
  [[ -z "$health_url" ]] || curl --fail --silent --show-error --max-time 10 "$health_url" >/dev/null
}

rollback_application() {
  local failed_service=$1
  if [[ "$before_version" != "$after_version" ]]; then
    echo "migration version changed; refusing image-only rollback" >&2
    echo "keep the healthy instance, restore $backup_path into a new database, then switch the database URL file" >&2
    return 1
  fi
  export LLMGATEWAY_GATEWAY_IMAGE=$old_image
  deployment_compose up --detach --no-deps --force-recreate --wait "$failed_service"
  check_public_health
}

for service in gateway-a gateway-b; do
  if ! deployment_compose up --detach --no-deps --force-recreate --wait "$service"; then
    rollback_application "$service"
    exit 1
  fi
  if ! check_public_health; then
    rollback_application "$service"
    exit 1
  fi
done

temporary_environment="$environment_file.partial"
found_image=false
while IFS= read -r line || [[ -n "$line" ]]; do
  if [[ "$line" == LLMGATEWAY_GATEWAY_IMAGE=* ]]; then
    printf 'LLMGATEWAY_GATEWAY_IMAGE=%s\n' "$new_image"
    found_image=true
  else
    printf '%s\n' "$line"
  fi
done < "$environment_file" > "$temporary_environment"
$found_image || { rm -f -- "$temporary_environment"; echo "environment file has no image entry" >&2; exit 1; }
chown --reference="$environment_file" "$temporary_environment"
chmod --reference="$environment_file" "$temporary_environment"
mv -- "$temporary_environment" "$environment_file"
systemctl reload llmgateway-compose.service
release_llmgateway_maintenance_lock
maintenance_lock_held=false
echo "LLMGateway rolling upgrade completed; pre-upgrade backup: $backup_path"
