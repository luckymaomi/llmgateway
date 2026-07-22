#!/usr/bin/env bash
set -euo pipefail

BACKUP_DEPLOY_DIRECTORY=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lib.sh
source "$BACKUP_DEPLOY_DIRECTORY/lib.sh"

declare -ar LLMGATEWAY_BACKUP_CONFIGURATION_FILES=(
  deployment.env
  secrets/postgres-password
  secrets/database-url
  secrets/valkey-password
  secrets/valkey-acl
  secrets/master-keys
  secrets/session-pepper
  secrets/api-key-pepper
  secrets/coordination-secret
)

backup_error() {
  echo "$1" >&2
  return 1
}

require_root_owned_path_ancestors() {
  local label=$1 path=$2 cursor mode
  cursor=$(dirname -- "$path")
  while [[ $cursor != / ]]; do
    [[ -d $cursor && ! -L $cursor && $(stat -c '%u' -- "$cursor") == 0 ]] || {
      backup_error "$label ancestor must be a root-owned directory"
      return 1
    }
    mode=$(stat -c '%a' -- "$cursor")
    (( (8#$mode & 8#0022) == 0 )) || {
      backup_error "$label ancestor must not be writable by group or world"
      return 1
    }
    cursor=$(dirname -- "$cursor")
  done
}

require_backup_control_file() {
  local label=$1 path=$2 mode canonical
  [[ $path == /* ]] || { backup_error "$label must be an absolute path"; return 1; }
  [[ -f $path && ! -L $path && -s $path ]] || { backup_error "$label must be a non-empty regular file"; return 1; }
  canonical=$(realpath "$path") || { backup_error "$label path cannot be resolved"; return 1; }
  [[ $canonical == "$path" ]] || { backup_error "$label path must not contain symbolic links"; return 1; }
  require_root_owned_path_ancestors "$label" "$path" || return 1
  [[ $(stat -c '%u' -- "$path") == 0 ]] || { backup_error "$label must be owned by UID 0"; return 1; }
  [[ $(stat -c '%h' -- "$path") == 1 ]] || { backup_error "$label must not be hard linked"; return 1; }
  (( $(stat -c '%s' -- "$path") <= 65536 )) || { backup_error "$label exceeds 64 KiB"; return 1; }
  mode=$(stat -c '%a' -- "$path")
  [[ $mode == 400 || $mode == 600 ]] || { backup_error "$label must have mode 0400 or 0600"; return 1; }
}

require_backup_directory() {
  local label=$1 path=$2 mode canonical
  [[ $path == /* && $path != / ]] || { backup_error "$label must be a non-root absolute path"; return 1; }
  [[ -d $path && ! -L $path ]] || { backup_error "$label must be a non-symbolic-link directory"; return 1; }
  canonical=$(realpath "$path") || { backup_error "$label path cannot be resolved"; return 1; }
  [[ $canonical == "$path" ]] || { backup_error "$label path must not contain symbolic links"; return 1; }
  require_root_owned_path_ancestors "$label" "$path" || return 1
  [[ $(stat -c '%u' -- "$path") == 0 ]] || { backup_error "$label must be owned by UID 0"; return 1; }
  mode=$(stat -c '%a' -- "$path")
  [[ $mode == 700 ]] || { backup_error "$label must have mode 0700"; return 1; }
}

configured_path() {
  local label=$1 path=$2 component cursor=''
  local -a components
  [[ $path == /* && $path != / ]] || { backup_error "$label must be a non-root absolute path"; return 1; }
  [[ $path != *//* && $path != */./* && $path != */../* && $path != */. && $path != */.. && $path != */ ]] || {
    backup_error "$label path must not contain empty or dot segments"
    return 1
  }
  IFS=/ read -r -a components <<< "$path"
  for component in "${components[@]}"; do
    [[ -z $component ]] && continue
    cursor+="/$component"
    [[ ! -L $cursor ]] || { backup_error "$label path must not contain symbolic links"; return 1; }
  done
  printf '%s' "$path"
}

paths_overlap() {
  local left=$1 right=$2
  [[ $left == "$right" || $left == "$right/"* || $right == "$left/"* ]]
}

remove_stale_private_directories() {
  local parent=$1 prefix=$2 allowed_modes=${3:-700} candidate name suffix parent_device candidate_mode
  local mount_point mount_conflict
  local -a candidates=()
  [[ -d $parent && ! -L $parent && $prefix =~ ^[A-Za-z0-9.-]{3,64}$ && $allowed_modes =~ ^[0-7]{3}(\|[0-7]{3})*$ ]] || {
    backup_error "stale directory cleanup contract is invalid"
    return 1
  }
  parent_device=$(stat -c '%d' -- "$parent") || return 1
  shopt -s nullglob
  candidates=("$parent/$prefix"*)
  shopt -u nullglob
  for candidate in "${candidates[@]}"; do
    name=${candidate##*/}
    suffix=${name#"$prefix"}
    [[ $name == "$prefix"* && $suffix =~ ^[A-Za-z0-9]{8}$ && -d $candidate && ! -L $candidate ]] || {
      backup_error "stale private directory has an unsafe name or type"
      return 1
    }
    candidate_mode=$(stat -c '%a' -- "$candidate")
    [[ $(stat -c '%u:%g:%d' -- "$candidate") == "0:0:$parent_device" && $candidate_mode =~ ^($allowed_modes)$ ]] || {
      backup_error "stale private directory has unsafe ownership, mode, or filesystem"
      return 1
    }
    mount_conflict=false
    while IFS=' ' read -r _ _ _ _ mount_point _; do
      mount_point=${mount_point//\\040/ }
      mount_point=${mount_point//\\011/$'\t'}
      mount_point=${mount_point//\\012/$'\n'}
      mount_point=${mount_point//\\134/\\}
      if [[ $mount_point == "$candidate" || $mount_point == "$candidate/"* ]]; then
        mount_conflict=true
        break
      fi
    done < /proc/self/mountinfo
    [[ $mount_conflict == false ]] || { backup_error "stale private directory is or contains a mount point"; return 1; }
    rm -rf -- "$candidate"
  done
}

require_distinct_control_files() {
  local path identity
  declare -A identities=()
  for path in "$@"; do
    identity=$(stat -c '%d:%i' -- "$path") || { backup_error "could not inspect backup control file"; return 1; }
    [[ -z ${identities[$identity]+x} ]] || { backup_error "backup control files must be distinct"; return 1; }
    identities[$identity]=$path
  done
}

read_repository_specification() {
  local path=$1 line
  local -a repository_lines
  mapfile -t repository_lines < "$path"
  [[ ${#repository_lines[@]} -eq 1 ]] || { backup_error "Restic repository file must contain exactly one line"; return 1; }
  line=${repository_lines[0]}
  [[ -n $line && $line != *[[:space:]]* ]] || { backup_error "Restic repository specification is invalid"; return 1; }
  printf '%s' "$line"
}

require_repository_policy() {
  local mode=$1 repository=$2 port
  case "$mode" in
    production)
      [[ $repository =~ ^s3:(https://[A-Za-z0-9.-]+(:[0-9]{1,5})?/[A-Za-z0-9._%+/-]+|s3[.-][A-Za-z0-9.-]+/[A-Za-z0-9._%+/-]+)$ ]] || {
        backup_error "production backups require a supported remote S3 Restic repository"
        return 1
      }
      [[ $repository != s3:https://localhost/* && $repository != s3:https://localhost:*/* &&
         $repository != s3:https://127.* && $repository != s3:https://0.0.0.0/* ]] || {
        backup_error "production S3 repository must not use a loopback endpoint"
        return 1
      }
      if [[ $repository =~ ^s3:https://[A-Za-z0-9.-]+:([0-9]{1,5})/ ]]; then
        port=${BASH_REMATCH[1]}
        (( 10#$port >= 1 && 10#$port <= 65535 )) || { backup_error "production S3 repository port is invalid"; return 1; }
      fi
      [[ -n ${LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE:-} ]] || {
        backup_error "production S3 backups require an AWS credentials file"
        return 1
      }
      ;;
    acceptance)
      [[ $repository == local:/repository ]] || { backup_error "acceptance backups require repository local:/repository"; return 1; }
      [[ ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY+x} == x && -n ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY:-} ]] || {
        backup_error "acceptance backups require an explicit local repository directory"
        return 1
      }
      require_backup_directory "local Restic repository directory" "$LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY" || return 1
      ;;
    *)
      backup_error "LLMGATEWAY_BACKUP_MODE must be production or acceptance"
      return 1
      ;;
  esac
}

verify_configuration_tree_contract() {
  local profile=$1 root=$2 entry relative contract kind expected_uid expected_gid expected_mode root_contract
  declare -A expected=()
  case "$profile" in
    runtime)
      root_contract=0:0:750
      expected=(
        [deployment.env]='file:0:0:640'
        [secrets]='directory:0:0:750'
        [secrets/postgres-password]='file:0:0:400'
        [secrets/database-url]='file:65532:65532:400'
        [secrets/valkey-password]='file:65532:65532:400'
        [secrets/valkey-acl]='file:999:1000:400'
        [secrets/master-keys]='file:65532:65532:400'
        [secrets/session-pepper]='file:65532:65532:400'
        [secrets/api-key-pepper]='file:65532:65532:400'
        [secrets/coordination-secret]='file:65532:65532:400'
      )
      ;;
    backup)
      root_contract=0:0:700
      expected=(
        [deployment.env]='file:0:0:400'
        [secrets]='directory:0:0:700'
        [secrets/postgres-password]='file:0:0:400'
        [secrets/database-url]='file:0:0:400'
        [secrets/valkey-password]='file:0:0:400'
        [secrets/valkey-acl]='file:0:0:400'
        [secrets/master-keys]='file:0:0:400'
        [secrets/session-pepper]='file:0:0:400'
        [secrets/api-key-pepper]='file:0:0:400'
        [secrets/coordination-secret]='file:0:0:400'
      )
      ;;
    *) backup_error "configuration tree profile is invalid"; return 1 ;;
  esac
  [[ -d $root && ! -L $root ]] || { backup_error "configuration directory must be a directory"; return 1; }
  [[ $(stat -c '%u:%g:%a' -- "$root") == "$root_contract" ]] || {
    backup_error "configuration directory owner or mode does not match its contract"
    return 1
  }
  while IFS= read -r -d '' entry; do
    relative=${entry#"$root/"}
    [[ -n ${expected[$relative]+x} ]] || { backup_error "configuration tree contains an unexpected entry"; return 1; }
    contract=${expected[$relative]}
    IFS=: read -r kind expected_uid expected_gid expected_mode <<< "$contract"
    if [[ $kind == directory ]]; then
      [[ -d $entry && ! -L $entry ]] || { backup_error "configuration tree contains a non-directory entry"; return 1; }
    else
      [[ -f $entry && ! -L $entry && -s $entry ]] || { backup_error "configuration secret must be a non-empty regular file"; return 1; }
      [[ $(stat -c '%h' -- "$entry") == 1 ]] || { backup_error "configuration files must not be hard linked"; return 1; }
      (( $(stat -c '%s' -- "$entry") <= 65536 )) || { backup_error "configuration file exceeds 64 KiB"; return 1; }
    fi
    [[ $(stat -c '%u:%g:%a' -- "$entry") == "$expected_uid:$expected_gid:$expected_mode" ]] || {
      backup_error "configuration entry owner or mode does not match its runtime contract"
      return 1
    }
    unset 'expected[$relative]'
  done < <(find "$root" -mindepth 1 -print0)
  (( ${#expected[@]} == 0 )) || { backup_error "configuration tree is incomplete"; return 1; }
}

verify_runtime_configuration_tree() {
  verify_configuration_tree_contract runtime "$1"
}

verify_backup_configuration_tree() {
  verify_configuration_tree_contract backup "$1"
}

require_configuration_bindings() {
  local root=$1 variable relative
  while IFS=: read -r variable relative; do
    [[ ${!variable:-} == "$root/$relative" ]] || {
      backup_error "$variable must point into the fixed configuration tree"
      return 1
    }
  done <<'EOF'
LLMGATEWAY_POSTGRES_PASSWORD_FILE:secrets/postgres-password
LLMGATEWAY_DATABASE_URL_FILE:secrets/database-url
LLMGATEWAY_VALKEY_PASSWORD_FILE:secrets/valkey-password
LLMGATEWAY_VALKEY_ACL_FILE:secrets/valkey-acl
LLMGATEWAY_MASTER_KEYS_FILE:secrets/master-keys
LLMGATEWAY_SESSION_PEPPER_FILE:secrets/session-pepper
LLMGATEWAY_API_KEY_PEPPER_FILE:secrets/api-key-pepper
LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE:secrets/coordination-secret
EOF
}

write_configuration_checksum() {
  local root=$1 output=$2 relative
  : > "$output"
  for relative in "${LLMGATEWAY_BACKUP_CONFIGURATION_FILES[@]}"; do
    (cd "$root" && sha256sum -- "$relative") >> "$output"
  done
}

verify_configuration_checksum() {
  local root=$1 checksum=$2 relative expected actual
  expected=''
  for relative in "${LLMGATEWAY_BACKUP_CONFIGURATION_FILES[@]}"; do
    actual=$(cd "$root" && sha256sum -- "$relative" | awk '{print $1}')
    expected+="$actual  $relative"$'\n'
  done
  printf '%s' "$expected" | cmp -s - "$checksum" || { backup_error "configuration checksum does not match configuration tree"; return 1; }
}

verify_backup_payload() {
  local payload=$1 entry relative kind mode expected_digest actual_digest
  local recovery_point recovery_point_parse migration_version gateway_image gateway_digest postgres_digest configured_gateway_image
  local -a manifest_lines
  declare -A expected=(
    [configuration]=directory
    [configuration/secrets]=directory
    [configuration/deployment.env]=file
    [configuration/secrets/postgres-password]=file
    [configuration/secrets/database-url]=file
    [configuration/secrets/valkey-password]=file
    [configuration/secrets/valkey-acl]=file
    [configuration/secrets/master-keys]=file
    [configuration/secrets/session-pepper]=file
    [configuration/secrets/api-key-pepper]=file
    [configuration/secrets/coordination-secret]=file
    [postgres.dump]=file
    [postgres.dump.sha256]=file
    [configuration.sha256]=file
    [backup-manifest]=file
  )
  [[ -d $payload && ! -L $payload ]] || { backup_error "backup payload must be a directory"; return 1; }
  [[ $(stat -c '%u' -- "$payload") == 0 ]] || { backup_error "backup payload must be owned by UID 0"; return 1; }
  mode=$(stat -c '%a' -- "$payload")
  (( (8#$mode & 8#7027) == 0 )) || { backup_error "backup payload has unsafe permissions"; return 1; }
  while IFS= read -r -d '' entry; do
    relative=${entry#"$payload/"}
    [[ -n ${expected[$relative]+x} ]] || { backup_error "backup payload contains an unexpected entry"; return 1; }
    kind=${expected[$relative]}
    if [[ $kind == directory ]]; then
      [[ -d $entry && ! -L $entry ]] || { backup_error "backup payload contains a non-directory entry"; return 1; }
    else
      [[ -f $entry && ! -L $entry && -s $entry ]] || { backup_error "backup payload file is missing or invalid"; return 1; }
      if [[ $relative != configuration/* ]]; then
        [[ $(stat -c '%u:%g' -- "$entry") == 0:0 ]] || { backup_error "backup metadata must be owned by 0:0"; return 1; }
        mode=$(stat -c '%a' -- "$entry")
        (( (8#$mode & 8#7077) == 0 )) || { backup_error "backup metadata has unsafe permissions"; return 1; }
      fi
    fi
    unset 'expected[$relative]'
  done < <(find "$payload" -mindepth 1 -print0)
  (( ${#expected[@]} == 0 )) || { backup_error "backup payload is incomplete"; return 1; }
  verify_backup_configuration_tree "$payload/configuration" || return 1
  verify_configuration_checksum "$payload/configuration" "$payload/configuration.sha256" || return 1
  actual_digest=$(sha256sum -- "$payload/postgres.dump" | awk '{print $1}')
  printf '%s  postgres.dump\n' "$actual_digest" | cmp -s - "$payload/postgres.dump.sha256" || { backup_error "PostgreSQL dump checksum does not match"; return 1; }
  expected_digest=$(sha256sum -- "$payload/configuration.sha256" | awk '{print $1}')
  postgres_digest=$actual_digest
  mapfile -t manifest_lines < "$payload/backup-manifest"
  [[ ${#manifest_lines[@]} -eq 7 ]] || { backup_error "backup manifest must contain exactly seven entries"; return 1; }
  [[ ${manifest_lines[0]} == format=llmgateway-backup ]] || { backup_error "backup manifest format is invalid"; return 1; }
  [[ ${manifest_lines[1]} =~ ^recovery_point_utc=([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)$ ]] || { backup_error "backup recovery point is invalid"; return 1; }
  recovery_point=${BASH_REMATCH[1]}
  recovery_point_parse=${recovery_point%Z}
  recovery_point_parse=${recovery_point_parse/T/ }
  [[ $(date -u -d "$recovery_point_parse" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null) == "$recovery_point" ]] || { backup_error "backup recovery point is not a real UTC time"; return 1; }
  [[ ${manifest_lines[2]} =~ ^migration_version=([0-9]+)$ ]] || { backup_error "backup migration version is invalid"; return 1; }
  migration_version=${BASH_REMATCH[1]}
  [[ ${manifest_lines[3]} =~ ^gateway_image=([^[:space:]]+)$ ]] || { backup_error "backup gateway image is invalid"; return 1; }
  gateway_image=${BASH_REMATCH[1]}
  [[ $gateway_image =~ @sha256:[a-f0-9]{64}$ ]] || { backup_error "backup gateway image is not immutable"; return 1; }
  configured_gateway_image=$(
    unset LLMGATEWAY_GATEWAY_IMAGE
    load_llmgateway_environment "$payload/configuration/deployment.env"
    printf '%s' "${LLMGATEWAY_GATEWAY_IMAGE:-}"
  ) || return 1
  [[ $gateway_image == "$configured_gateway_image" ]] || { backup_error "backup gateway image does not match deployment.env"; return 1; }
  gateway_digest=${gateway_image##*@}
  [[ ${manifest_lines[4]} == "gateway_image_digest=$gateway_digest" ]] || { backup_error "gateway image digest is inconsistent"; return 1; }
  [[ ${manifest_lines[5]} == "configuration_sha256=sha256:$expected_digest" ]] || { backup_error "configuration digest is inconsistent"; return 1; }
  [[ ${manifest_lines[6]} == "postgres_dump_sha256=sha256:$postgres_digest" ]] || { backup_error "PostgreSQL digest is inconsistent"; return 1; }
  printf '%s\n' "${manifest_lines[@]}" | cmp -s - "$payload/backup-manifest" || { backup_error "backup manifest is not canonical"; return 1; }
}

load_backup_environment() {
  local file=$1 configuration_directory staging_root marker_file deployment_file repository_spec
  local -a control_files=()
  [[ $EUID -eq 0 ]] || { backup_error "backup operations require UID 0"; return 1; }
  require_backup_control_file "backup environment" "$file" || return 1
  unset LLMGATEWAY_BACKUP_MODE LLMGATEWAY_RESTIC_IMAGE LLMGATEWAY_RESTIC_REPOSITORY_FILE \
    LLMGATEWAY_RESTIC_PASSWORD_FILE LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE \
    LLMGATEWAY_RESTIC_AWS_CONFIG_FILE LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY \
    LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE LLMGATEWAY_CONFIGURATION_DIRECTORY \
    LLMGATEWAY_BACKUP_STAGING_ROOT LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE \
    LLMGATEWAY_RESTIC_CHECK_SUBSET
  load_llmgateway_environment "$file"
  : "${LLMGATEWAY_BACKUP_MODE:?set LLMGATEWAY_BACKUP_MODE}"
  : "${LLMGATEWAY_RESTIC_IMAGE:?set the immutable Restic image}"
  : "${LLMGATEWAY_RESTIC_REPOSITORY_FILE:?set the Restic repository file}"
  : "${LLMGATEWAY_RESTIC_PASSWORD_FILE:?set the Restic password file}"
  : "${LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE:?set the deployment environment file}"
  : "${LLMGATEWAY_CONFIGURATION_DIRECTORY:?set the configuration directory}"
  : "${LLMGATEWAY_BACKUP_STAGING_ROOT:?set the backup staging root}"
  : "${LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE:?set the backup success marker}"
  [[ $LLMGATEWAY_RESTIC_IMAGE =~ @sha256:[a-f0-9]{64}$ ]] || { backup_error "Restic image must be immutable"; return 1; }
  : "${LLMGATEWAY_RESTIC_CHECK_SUBSET:=5%}"
  [[ $LLMGATEWAY_RESTIC_CHECK_SUBSET =~ ^(100|[1-9][0-9]?)%$ ]] || {
    backup_error "Restic check subset must be between 1% and 100%"
    return 1
  }
  export LLMGATEWAY_RESTIC_CHECK_SUBSET
  configuration_directory=$(configured_path "configuration directory" "$LLMGATEWAY_CONFIGURATION_DIRECTORY") || return 1
  staging_root=$(configured_path "backup staging root" "$LLMGATEWAY_BACKUP_STAGING_ROOT") || return 1
  marker_file=$(configured_path "backup success marker" "$LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE") || return 1
  deployment_file=$(configured_path "deployment environment file" "$LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE") || return 1
  [[ $deployment_file == "$configuration_directory/deployment.env" ]] || { backup_error "deployment environment must be configuration/deployment.env"; return 1; }
  require_backup_directory "backup staging root" "$staging_root" || return 1
  [[ $marker_file == "$staging_root/last-success" ]] || { backup_error "backup success marker must be staging-root/last-success"; return 1; }
  if [[ -e $marker_file || -L $marker_file ]]; then
    require_backup_control_file "backup success marker" "$marker_file" || return 1
  fi
  LLMGATEWAY_CONFIGURATION_DIRECTORY=$configuration_directory
  LLMGATEWAY_BACKUP_STAGING_ROOT=$staging_root
  LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE=$marker_file
  LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE=$deployment_file
  export LLMGATEWAY_CONFIGURATION_DIRECTORY LLMGATEWAY_BACKUP_STAGING_ROOT \
    LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE LLMGATEWAY_DEPLOYMENT_ENVIRONMENT_FILE
  require_backup_control_file "Restic repository file" "$LLMGATEWAY_RESTIC_REPOSITORY_FILE" || return 1
  require_backup_control_file "Restic password file" "$LLMGATEWAY_RESTIC_PASSWORD_FILE" || return 1
  LLMGATEWAY_RESTIC_REPOSITORY_FILE=$(realpath "$LLMGATEWAY_RESTIC_REPOSITORY_FILE")
  LLMGATEWAY_RESTIC_PASSWORD_FILE=$(realpath "$LLMGATEWAY_RESTIC_PASSWORD_FILE")
  control_files=("$file" "$LLMGATEWAY_RESTIC_REPOSITORY_FILE" "$LLMGATEWAY_RESTIC_PASSWORD_FILE")
  for variable in LLMGATEWAY_RESTIC_AWS_CREDENTIALS_FILE LLMGATEWAY_RESTIC_AWS_CONFIG_FILE; do
    if [[ ${!variable+x} == x ]]; then
      [[ -n ${!variable} ]] || { backup_error "$variable cannot be empty"; return 1; }
      require_backup_control_file "$variable" "${!variable}" || return 1
      printf -v "$variable" '%s' "$(realpath "${!variable}")"
      control_files+=("${!variable}")
    fi
  done
  require_distinct_control_files "${control_files[@]}" || return 1
  for path in "${control_files[@]}"; do
    if paths_overlap "$path" "$configuration_directory"; then backup_error "backup control file overlaps configuration directory"; return 1; fi
    if paths_overlap "$path" "$staging_root"; then backup_error "backup control file overlaps staging root"; return 1; fi
    if [[ ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY+x} == x && -n ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY:-} ]]; then
      if paths_overlap "$path" "$(realpath "$LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY")"; then backup_error "backup control file overlaps local repository"; return 1; fi
    fi
  done
  if paths_overlap "$configuration_directory" "$staging_root"; then backup_error "configuration and staging paths overlap"; return 1; fi
  repository_spec=$(read_repository_specification "$LLMGATEWAY_RESTIC_REPOSITORY_FILE") || return 1
  require_repository_policy "$LLMGATEWAY_BACKUP_MODE" "$repository_spec" || return 1
  if [[ $LLMGATEWAY_BACKUP_MODE == production && ${LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY+x} == x ]]; then
    backup_error "production backups must not set a local repository directory"
    return 1
  fi
  if [[ $LLMGATEWAY_BACKUP_MODE == acceptance ]]; then
    if paths_overlap "$configuration_directory" "$(realpath "$LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY")"; then backup_error "configuration and local repository paths overlap"; return 1; fi
    if paths_overlap "$staging_root" "$(realpath "$LLMGATEWAY_RESTIC_LOCAL_REPOSITORY_DIRECTORY")"; then backup_error "staging and local repository paths overlap"; return 1; fi
  fi
}

check_backup_freshness() {
  local marker=${LLMGATEWAY_BACKUP_LAST_SUCCESS_MARKER_FILE:?} recovery_point recovery_epoch now_epoch age_seconds
  local -a marker_lines
  if [[ ! -e $marker ]]; then
    echo "backup success marker is absent; a first successful backup is required" >&2
    return 1
  fi
  mapfile -t marker_lines < "$marker"
  if [[ ${#marker_lines[@]} -ne 3 || ${marker_lines[0]} != format=llmgateway-backup-success ||
        ! ${marker_lines[1]} =~ ^recovery_point_utc=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ||
        ! ${marker_lines[2]} =~ ^completed_at_utc=([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)$ ]]; then
    logger --priority daemon.alert --tag llmgateway-backup "LLMGateway backup success marker is invalid" 2>/dev/null || true
    echo "backup success marker is invalid" >&2
    return 1
  fi
  recovery_point=${marker_lines[1]#recovery_point_utc=}
  recovery_epoch=$(date -u -d "${recovery_point%Z}" +%s 2>/dev/null) || recovery_epoch=0
  now_epoch=$(date -u +%s)
  age_seconds=$(( now_epoch - recovery_epoch ))
  if (( recovery_epoch == 0 || age_seconds < 0 || age_seconds > 21600 )); then
    logger --priority daemon.alert --tag llmgateway-backup "LLMGateway backup freshness exceeded the six-hour objective" 2>/dev/null || true
    echo "backup freshness exceeded the six-hour objective" >&2
    return 1
  fi
  printf 'LLMGateway backup recovery point age: %ss\n' "$age_seconds"
}

require_restic_run_owner() {
  [[ $1 =~ ^[a-z0-9][a-z0-9.-]{2,63}$ ]] || {
    backup_error "Restic run owner is invalid"
    return 1
  }
}

cleanup_restic_execution() {
  local run_owner=$1 runtime_root=/run/llmgateway-restic run_directory container_ids remaining_ids container_id
  local mount_point unexpected_runtime_entry
  require_restic_run_owner "$run_owner" || return 1
  run_directory=$runtime_root/$run_owner

  if ! container_ids=$(timeout --signal=TERM --kill-after=5s 20s docker ps --all --quiet --no-trunc \
      --filter 'label=com.llmgateway.restic.owner' \
      --filter "label=com.llmgateway.restic.owner=$run_owner"); then
    backup_error "could not enumerate owned Restic containers"
    return 1
  fi
  while IFS= read -r container_id; do
    [[ -z $container_id ]] && continue
    [[ $container_id =~ ^[a-f0-9]{64}$ ]] || { backup_error "Docker returned an invalid Restic container ID"; return 1; }
    timeout --signal=TERM --kill-after=5s 20s docker rm --force "$container_id" >/dev/null 2>&1 || true
  done <<< "$container_ids"
  if ! remaining_ids=$(timeout --signal=TERM --kill-after=5s 20s docker ps --all --quiet --no-trunc \
      --filter 'label=com.llmgateway.restic.owner' \
      --filter "label=com.llmgateway.restic.owner=$run_owner"); then
    backup_error "could not verify owned Restic container cleanup"
    return 1
  fi
  [[ -z $remaining_ids ]] || { backup_error "owned Restic container survived forced cleanup"; return 1; }

  if [[ -e $runtime_root || -L $runtime_root ]]; then
    require_backup_directory "Restic runtime root" "$runtime_root" || return 1
  fi
  if [[ -e $run_directory || -L $run_directory ]]; then
    [[ -d $run_directory && ! -L $run_directory && $(stat -c '%u:%g:%a' -- "$run_directory") == 0:0:700 ]] || {
      backup_error "Restic runtime directory is unsafe"
      return 1
    }
    while IFS=' ' read -r _ _ _ _ mount_point _; do
      mount_point=${mount_point//\\040/ }
      mount_point=${mount_point//\\011/$'\t'}
      mount_point=${mount_point//\\012/$'\n'}
      mount_point=${mount_point//\\134/\\}
      [[ $mount_point != "$run_directory" && $mount_point != "$run_directory/"* ]] || {
        backup_error "Restic runtime directory is or contains a mount point"
        return 1
      }
    done < /proc/self/mountinfo
    unexpected_runtime_entry=$(find "$run_directory" -mindepth 1 -maxdepth 1 ! -name container-id -print -quit)
    [[ -z $unexpected_runtime_entry ]] || { backup_error "Restic runtime directory contains an unexpected entry"; return 1; }
    if [[ -e $run_directory/container-id || -L $run_directory/container-id ]]; then
      [[ -f $run_directory/container-id && ! -L $run_directory/container-id &&
         $(stat -c '%u:%g:%h' -- "$run_directory/container-id") == 0:0:1 ]] || {
        backup_error "Restic container ID file is unsafe"
        return 1
      }
      rm -- "$run_directory/container-id"
    fi
    rmdir -- "$run_directory"
  fi
}

run_restic() {
  local run_owner=${LLMGATEWAY_RESTIC_RUN_OWNER:-} runtime_root=/run/llmgateway-restic random_run_id
  local run_directory container_id_file container_name
  local mounts=(
    --mount "type=bind,source=$LLMGATEWAY_RESTIC_REPOSITORY_FILE,target=/run/secrets/restic-repository,readonly"
    --mount "type=bind,source=$LLMGATEWAY_RESTIC_PASSWORD_FILE,target=/run/secrets/restic-password,readonly"
  )
  local environment=() capabilities=(--cap-drop ALL)
  if [[ -z $run_owner ]]; then
    random_run_id=$(< /proc/sys/kernel/random/uuid)
    run_owner=manual-$random_run_id
  fi
  require_restic_run_owner "$run_owner" || return 1
  if [[ ${RESTIC_ALLOW_CHOWN:-} == true ]]; then
    capabilities+=(--cap-add CHOWN)
  elif [[ -n ${RESTIC_ALLOW_CHOWN:-} ]]; then
    backup_error "RESTIC_ALLOW_CHOWN must be empty or true"
    return 1
  fi
  if [[ $LLMGATEWAY_BACKUP_MODE == acceptance ]]; then
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
    [[ $RESTIC_DATA_MOUNT_SOURCE == /* && -d $RESTIC_DATA_MOUNT_SOURCE ]] || { backup_error "Restic data source is invalid"; return 1; }
    [[ ${RESTIC_DATA_MOUNT_TARGET:-} == /* ]] || { backup_error "Restic data target is invalid"; return 1; }
    mounts+=(--mount "type=bind,source=$RESTIC_DATA_MOUNT_SOURCE,target=$RESTIC_DATA_MOUNT_TARGET${RESTIC_DATA_MOUNT_READONLY:+,readonly}")
  fi
  if [[ ! -e $runtime_root && ! -L $runtime_root ]]; then
    install -d -o 0 -g 0 -m 0700 "$runtime_root"
  fi
  require_backup_directory "Restic runtime root" "$runtime_root" || return 1
  cleanup_restic_execution "$run_owner" || return 1
  run_directory=$runtime_root/$run_owner
  install -d -o 0 -g 0 -m 0700 "$run_directory"
  container_id_file=$run_directory/container-id
  container_name=llmgateway-restic-$run_owner
  (
    cleanup_restic_subprocess() {
      local status=$?
      trap - EXIT
      cleanup_restic_execution "$run_owner" || status=1
      exit "$status"
    }
    trap cleanup_restic_subprocess EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM
    umask 0077
    docker run --rm --read-only --name "$container_name" --cidfile "$container_id_file" \
      --label "com.llmgateway.restic.owner=$run_owner" \
      "${capabilities[@]}" --security-opt no-new-privileges \
      --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
      "${mounts[@]}" "${environment[@]}" "$LLMGATEWAY_RESTIC_IMAGE" \
      --no-cache --repository-file /run/secrets/restic-repository --password-file /run/secrets/restic-password "$@"
  )
}
