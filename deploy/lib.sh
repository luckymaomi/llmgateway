#!/usr/bin/env bash
set -euo pipefail

load_llmgateway_environment() {
  local file=$1 line key value
  [[ -f "$file" ]] || { echo "environment file does not exist: $file" >&2; return 1; }
  while IFS= read -r line || [[ -n "$line" ]]; do
    line=${line%$'\r'}
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" == *=* ]] || { echo "invalid environment entry" >&2; return 1; }
    key=${line%%=*}
    value=${line#*=}
    [[ "$key" =~ ^LLMGATEWAY_[A-Z0-9_]+$ ]] || { echo "invalid environment key" >&2; return 1; }
    export "$key=$value"
  done < "$file"
}

require_file_secrets() {
  local name path
  for name in \
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
