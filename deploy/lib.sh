#!/usr/bin/env bash
set -euo pipefail

load_llmgateway_environment() {
  local file=$1 line key value
  local -A seen_keys=()
  [[ -f "$file" ]] || { echo "environment file does not exist: $file" >&2; return 1; }
  [[ ! -L "$file" ]] || { echo "environment file must not be a symbolic link: $file" >&2; return 1; }
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=${line%$'\r'}
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" == *=* ]] || { echo "invalid environment entry" >&2; return 1; }
    key=${line%%=*}
    value=${line#*=}
    [[ "$key" =~ ^LLMGATEWAY_[A-Z0-9_]+$ ]] || { echo "invalid environment key" >&2; return 1; }
    [[ -z ${seen_keys[$key]+x} ]] || { echo "duplicate environment key: $key" >&2; return 1; }
    seen_keys[$key]=true
    export "$key=$value"
  done < "$file"
}

acquire_llmgateway_maintenance_lock() {
  local operation=$1 lock_file=/run/lock/llmgateway-maintenance.lock
  local path_identity descriptor_identity
  [[ $(id -u) -eq 0 ]] || { echo "maintenance operations require root" >&2; return 1; }
  [[ $operation =~ ^[a-z][a-z0-9-]{1,31}$ ]] || { echo "maintenance operation name is invalid" >&2; return 1; }
  [[ ${LLMGATEWAY_MAINTENANCE_LOCK_HELD:-false} != true ]] || {
    echo "this process already holds the LLMGateway maintenance lock" >&2
    return 1
  }
  [[ -d /run/lock && ! -L /run/lock ]] || { echo "/run/lock is unavailable or unsafe" >&2; return 1; }

  if [[ ! -e $lock_file && ! -L $lock_file ]]; then
    (umask 0077; set -o noclobber; : > "$lock_file") 2>/dev/null || true
  fi
  [[ -f $lock_file && ! -L $lock_file ]] || { echo "maintenance lock file is unsafe" >&2; return 1; }
  [[ $(stat -c %u "$lock_file") -eq 0 && $(stat -c %a "$lock_file") == 600 ]] || {
    echo "maintenance lock file must be root-owned with mode 0600" >&2
    return 1
  }

  exec {LLMGATEWAY_MAINTENANCE_LOCK_FD}<>"$lock_file"
  path_identity=$(stat -Lc '%d:%i' "$lock_file")
  descriptor_identity=$(stat -Lc '%d:%i' "/proc/$$/fd/$LLMGATEWAY_MAINTENANCE_LOCK_FD")
  if [[ $path_identity != "$descriptor_identity" ]]; then
    exec {LLMGATEWAY_MAINTENANCE_LOCK_FD}>&-
    echo "maintenance lock file changed while it was opened" >&2
    return 1
  fi
  if ! flock -n "$LLMGATEWAY_MAINTENANCE_LOCK_FD"; then
    exec {LLMGATEWAY_MAINTENANCE_LOCK_FD}>&-
    echo "another LLMGateway maintenance operation is running" >&2
    return 1
  fi
  LLMGATEWAY_MAINTENANCE_LOCK_HELD=true
  LLMGATEWAY_MAINTENANCE_OPERATION=$operation
}

release_llmgateway_maintenance_lock() {
  [[ ${LLMGATEWAY_MAINTENANCE_LOCK_HELD:-false} == true ]] || return 0
  [[ ${LLMGATEWAY_MAINTENANCE_LOCK_FD:-} =~ ^[0-9]+$ ]] || {
    echo "maintenance lock descriptor is invalid" >&2
    return 1
  }
  flock -u "$LLMGATEWAY_MAINTENANCE_LOCK_FD"
  exec {LLMGATEWAY_MAINTENANCE_LOCK_FD}>&-
  unset LLMGATEWAY_MAINTENANCE_LOCK_HELD LLMGATEWAY_MAINTENANCE_OPERATION
}

require_file_secrets() {
  local name path
  for name in \
    LLMGATEWAY_POSTGRES_PASSWORD \
    LLMGATEWAY_DATABASE_URL \
    LLMGATEWAY_VALKEY_PASSWORD \
    LLMGATEWAY_MASTER_KEYS \
    LLMGATEWAY_SESSION_PEPPER \
    LLMGATEWAY_API_KEY_PEPPER \
    LLMGATEWAY_COORDINATION_KEY_HASH_SECRET; do
    [[ -z ${!name+x} ]] || { echo "$name must use its _FILE input" >&2; return 1; }
  done
  for name in \
    LLMGATEWAY_POSTGRES_PASSWORD_FILE \
    LLMGATEWAY_DATABASE_URL_FILE \
    LLMGATEWAY_VALKEY_PASSWORD_FILE \
    LLMGATEWAY_VALKEY_ACL_FILE \
    LLMGATEWAY_MASTER_KEYS_FILE \
    LLMGATEWAY_SESSION_PEPPER_FILE \
    LLMGATEWAY_API_KEY_PEPPER_FILE \
    LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE; do
    path=${!name:-}
    [[ -f "$path" && -s "$path" ]] || { echo "$name must name a non-empty file" >&2; return 1; }
    [[ $(stat -c %s "$path") -le 65536 ]] || { echo "$name exceeds 64 KiB" >&2; return 1; }
  done
}

require_immutable_gateway_image() {
  local name
  for name in LLMGATEWAY_GATEWAY_IMAGE LLMGATEWAY_POSTGRES_IMAGE LLMGATEWAY_VALKEY_IMAGE LLMGATEWAY_CADDY_IMAGE; do
    [[ ${!name:-} =~ @sha256:[a-f0-9]{64}$ ]] || {
      echo "$name must be an immutable sha256 reference" >&2
      return 1
    }
  done
}

deployment_compose() {
  local project=${LLMGATEWAY_COMPOSE_PROJECT:-llmgateway-production}
  [[ $project =~ ^[a-z0-9][a-z0-9_-]{1,62}$ ]] || {
    echo "LLMGATEWAY_COMPOSE_PROJECT is invalid" >&2
    return 1
  }
  docker compose --project-name "$project" --file "$DEPLOY_DIRECTORY/compose.production.yaml" "$@"
}
