#!/usr/bin/env bash
set -euo pipefail

BACKUP_DEPLOY_DIRECTORY=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$BACKUP_DEPLOY_DIRECTORY/lib.sh"

load_backup_environment() {
  local file=$1
  load_llmgateway_environment "$file"
  : "${LLMGATEWAY_RESTIC_IMAGE:?set the immutable Restic image}"
  : "${LLMGATEWAY_RESTIC_REPOSITORY_FILE:?set the Restic repository file}"
  : "${LLMGATEWAY_RESTIC_PASSWORD_FILE:?set the Restic password file}"
  [[ $LLMGATEWAY_RESTIC_IMAGE =~ @sha256:[a-f0-9]{64}$ ]] || {
    echo "LLMGATEWAY_RESTIC_IMAGE must be an immutable sha256 reference" >&2
    return 1
  }
  require_backup_secret_file "$LLMGATEWAY_RESTIC_REPOSITORY_FILE"
  require_backup_secret_file "$LLMGATEWAY_RESTIC_PASSWORD_FILE"
  if [[ -n ${LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE:-} ]]; then
    require_backup_secret_file "$LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE"
  fi
  if [[ -n ${LLMGATEWAY_RESTIC_AWS_CONFIG_FILE:-} ]]; then
    require_backup_secret_file "$LLMGATEWAY_RESTIC_AWS_CONFIG_FILE"
  fi
  if [[ -n ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY:-} ]]; then
    [[ -d $LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY ]] || {
      echo "local Restic repository directory does not exist" >&2
      return 1
    }
  fi
}

require_backup_secret_file() {
  local path=$1 mode
  [[ -f $path && -s $path ]] || { echo "backup secret file is missing or empty" >&2; return 1; }
  [[ $(stat -c %s "$path") -le 65536 ]] || { echo "backup secret file exceeds 64 KiB" >&2; return 1; }
  mode=$(stat -c %a "$path")
  (( (8#$mode & 8#077) == 0 )) || { echo "backup secret file must not be group/world accessible" >&2; return 1; }
}

run_restic() {
  local mounts=(
    --mount "type=bind,source=$LLMGATEWAY_RESTIC_REPOSITORY_FILE,target=/run/secrets/restic-repository,readonly"
    --mount "type=bind,source=$LLMGATEWAY_RESTIC_PASSWORD_FILE,target=/run/secrets/restic-password,readonly"
  )
  local environment=()
  local capabilities=(--cap-drop ALL)
  if [[ ${RESTIC_ALLOW_CHOWN:-} == true ]]; then
    capabilities+=(--cap-add CHOWN)
  elif [[ -n ${RESTIC_ALLOW_CHOWN:-} ]]; then
    echo "RESTIC_ALLOW_CHOWN must be empty or true" >&2
    return 1
  fi
  if [[ -n ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY:-} ]]; then
    mounts+=(--mount "type=bind,source=$LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY,target=/repository")
  fi
  if [[ -n ${LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE:-} ]]; then
    mounts+=(--mount "type=bind,source=$LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE,target=/run/secrets/aws-credentials,readonly")
    environment+=(--env AWS_SHARED_CREDENTIALS_FILE=/run/secrets/aws-credentials)
  fi
  if [[ -n ${LLMGATEWAY_RESTIC_AWS_CONFIG_FILE:-} ]]; then
    mounts+=(--mount "type=bind,source=$LLMGATEWAY_RESTIC_AWS_CONFIG_FILE,target=/run/secrets/aws-config,readonly")
    environment+=(--env AWS_CONFIG_FILE=/run/secrets/aws-config)
  fi
  if [[ -n ${RESTIC_DATA_MOUNT_SOURCE:-} ]]; then
    mounts+=(--mount "type=bind,source=$RESTIC_DATA_MOUNT_SOURCE,target=$RESTIC_DATA_MOUNT_TARGET${RESTIC_DATA_MOUNT_READONLY:+,readonly}")
  fi
  docker run --rm --read-only "${capabilities[@]}" --security-opt no-new-privileges \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
    "${mounts[@]}" "${environment[@]}" "$LLMGATEWAY_RESTIC_IMAGE" \
    --no-cache --repository-file /run/secrets/restic-repository --password-file /run/secrets/restic-password "$@"
}
