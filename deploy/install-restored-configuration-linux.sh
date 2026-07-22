#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 5 && $5 == --confirm-restored-configuration-install ]] || {
  echo "usage: $0 RESTORED_CONFIGURATION EMPTY_TARGET_CONFIGURATION NEW_DATABASE_URL_FILE TARGET_DATABASE --confirm-restored-configuration-install" >&2
  exit 2
}
[[ $EUID -eq 0 ]] || { echo "restored configuration installation requires root" >&2; exit 1; }

SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=backup-lib.sh
source "$SCRIPT_DIRECTORY/backup-lib.sh"

source_configuration=$(configured_path "restored configuration" "$1")
target_configuration=$(configured_path "target configuration" "$2")
database_url_file=$(configured_path "new database URL file" "$3")
target_database=$4

[[ $target_database =~ ^[A-Za-z][A-Za-z0-9_]{2,62}$ && $target_database != postgres && $target_database != template* ]] || {
  echo "unsafe target database name" >&2
  exit 1
}
[[ -d $source_configuration && ! -L $source_configuration && $(realpath -e -- "$source_configuration") == "$source_configuration" ]] || {
  echo "restored configuration must be an existing non-symbolic-link directory" >&2
  exit 1
}
require_root_owned_path_ancestors "restored configuration" "$source_configuration"
[[ ! -e $target_configuration && ! -L $target_configuration ]] || {
  echo "target configuration directory must not exist" >&2
  exit 1
}
target_parent=$(dirname -- "$target_configuration")
[[ -d $target_parent && ! -L $target_parent && $(realpath -e -- "$target_parent") == "$target_parent" ]] || {
  echo "target configuration parent is invalid" >&2
  exit 1
}
[[ $(stat -c '%u' -- "$target_parent") == 0 ]] || { echo "target configuration parent must be owned by UID 0" >&2; exit 1; }
target_parent_mode=$(stat -c '%a' -- "$target_parent")
(( (8#$target_parent_mode & 8#7022) == 0 )) || { echo "target configuration parent has unsafe permissions" >&2; exit 1; }
require_root_owned_path_ancestors "target configuration" "$target_configuration"

paths_overlap "$source_configuration" "$target_configuration" && {
  echo "source and target configuration paths must not overlap" >&2
  exit 1
}
for configuration_path in "$source_configuration" "$target_configuration"; do
  paths_overlap "$database_url_file" "$configuration_path" && {
    echo "new database URL file must be outside source and target configuration trees" >&2
    exit 1
  }
done

[[ -f $database_url_file && ! -L $database_url_file && $(realpath -e -- "$database_url_file") == "$database_url_file" ]] || {
  echo "new database URL file must be a non-symbolic-link regular file" >&2
  exit 1
}
[[ $(stat -c '%u' -- "$database_url_file") == 0 ]] || { echo "new database URL file must be owned by UID 0" >&2; exit 1; }
database_url_mode=$(stat -c '%a' -- "$database_url_file")
[[ $database_url_mode == 400 || $database_url_mode == 600 ]] || {
  echo "new database URL file must have mode 0400 or 0600" >&2
  exit 1
}
[[ $(stat -c '%h' -- "$database_url_file") == 1 ]] || { echo "new database URL file must not be hard linked" >&2; exit 1; }
require_root_owned_path_ancestors "new database URL file" "$database_url_file"
[[ -s $database_url_file && $(stat -c '%s' -- "$database_url_file") -le 65536 ]] || {
  echo "new database URL file is empty or exceeds 64 KiB" >&2
  exit 1
}

mapfile -t database_url_lines < "$database_url_file"
[[ ${#database_url_lines[@]} -eq 1 && -n ${database_url_lines[0]} && ${database_url_lines[0]} != *$'\r'* ]] || {
  echo "new database URL file must contain exactly one canonical line" >&2
  exit 1
}
database_url=${database_url_lines[0]}
[[ $database_url =~ ^postgres://([^/[:space:]?#]+)/([A-Za-z][A-Za-z0-9_]{2,62})(\?[^#[:space:]]+)?$ ]] || {
  echo "new database URL must be a canonical postgres URI" >&2
  exit 1
}
database_authority=${BASH_REMATCH[1]}
database_name=${BASH_REMATCH[2]}
[[ $database_name == "$target_database" ]] || { echo "new database URL does not name the target database" >&2; exit 1; }
[[ $database_authority == *@* && ${database_authority#*@} != *'@'* ]] || {
  echo "new database URL authority is invalid" >&2
  exit 1
}
database_userinfo=${database_authority%%@*}
database_hostport=${database_authority#*@}
[[ $database_userinfo =~ ^[A-Za-z0-9._~%+-]+:[^@/?#[:space:]]+$ ]] || {
  echo "new database URL user information is invalid" >&2
  exit 1
}
[[ $database_hostport =~ ^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9._-]+)(:([0-9]{1,5}))?$ ]] || {
  echo "new database URL host is invalid" >&2
  exit 1
}
if [[ -n ${BASH_REMATCH[3]:-} ]]; then
  database_port=${BASH_REMATCH[3]}
  (( 10#$database_port >= 1 && 10#$database_port <= 65535 )) || {
    echo "new database URL port is invalid" >&2
    exit 1
  }
fi
unset database_url database_url_lines database_userinfo database_hostport database_authority

verify_backup_configuration_tree "$source_configuration"

declare -A replacements=(
  [LLMGATEWAY_POSTGRES_DB]="$target_database"
  [LLMGATEWAY_POSTGRES_PASSWORD_FILE]="$target_configuration/secrets/postgres-password"
  [LLMGATEWAY_DATABASE_URL_FILE]="$target_configuration/secrets/database-url"
  [LLMGATEWAY_VALKEY_PASSWORD_FILE]="$target_configuration/secrets/valkey-password"
  [LLMGATEWAY_VALKEY_ACL_FILE]="$target_configuration/secrets/valkey-acl"
  [LLMGATEWAY_MASTER_KEYS_FILE]="$target_configuration/secrets/master-keys"
  [LLMGATEWAY_SESSION_PEPPER_FILE]="$target_configuration/secrets/session-pepper"
  [LLMGATEWAY_API_KEY_PEPPER_FILE]="$target_configuration/secrets/api-key-pepper"
  [LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE]="$target_configuration/secrets/coordination-secret"
)
declare -A seen_keys=()

maintenance_lock_held=false
acquire_llmgateway_maintenance_lock configuration-restore
maintenance_lock_held=true
remove_stale_private_directories "$target_parent" .llmgateway-configuration. '700|750'
staging_configuration=$(mktemp -d "$target_parent/.llmgateway-configuration.XXXXXXXX")
cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n ${staging_configuration:-} && $staging_configuration == "$target_parent/.llmgateway-configuration."* && -d $staging_configuration && ! -L $staging_configuration ]]; then
    rm -rf -- "$staging_configuration"
  fi
  if [[ $maintenance_lock_held == true ]]; then
    release_llmgateway_maintenance_lock || status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

install -d -o 0 -g 0 -m 0750 "$staging_configuration/secrets"
while IFS= read -r line || [[ -n $line ]]; do
  line=${line%$'\r'}
  if [[ -z $line || $line == \#* ]]; then
    printf '%s\n' "$line" >> "$staging_configuration/deployment.env"
    continue
  fi
  [[ $line == *=* ]] || { echo "restored deployment environment contains an invalid entry" >&2; exit 1; }
  key=${line%%=*}
  [[ $key =~ ^LLMGATEWAY_[A-Z0-9_]+$ ]] || { echo "restored deployment environment contains an invalid key" >&2; exit 1; }
  [[ -z ${seen_keys[$key]+x} ]] || { echo "restored deployment environment contains a duplicate key" >&2; exit 1; }
  seen_keys[$key]=true
  case "$key" in
    LLMGATEWAY_DATABASE_URL|LLMGATEWAY_VALKEY_PASSWORD|LLMGATEWAY_MASTER_KEYS|LLMGATEWAY_SESSION_PEPPER|LLMGATEWAY_API_KEY_PEPPER|LLMGATEWAY_COORDINATION_KEY_HASH_SECRET)
      echo "restored deployment environment contains an inline secret" >&2
      exit 1
      ;;
  esac
  if [[ -n ${replacements[$key]+x} ]]; then
    printf '%s=%s\n' "$key" "${replacements[$key]}" >> "$staging_configuration/deployment.env"
  else
    printf '%s\n' "$line" >> "$staging_configuration/deployment.env"
  fi
done < "$source_configuration/deployment.env"

for key in "${!replacements[@]}"; do
  [[ -n ${seen_keys[$key]+x} ]] || { echo "restored deployment environment is missing a required key" >&2; exit 1; }
done

install -o 0 -g 0 -m 0400 "$source_configuration/secrets/postgres-password" "$staging_configuration/secrets/postgres-password"
install -o 65532 -g 65532 -m 0400 "$database_url_file" "$staging_configuration/secrets/database-url"
for secret_name in valkey-password master-keys session-pepper api-key-pepper coordination-secret; do
  install -o 65532 -g 65532 -m 0400 "$source_configuration/secrets/$secret_name" "$staging_configuration/secrets/$secret_name"
done
install -o 999 -g 1000 -m 0400 "$source_configuration/secrets/valkey-acl" "$staging_configuration/secrets/valkey-acl"
chown 0:0 "$staging_configuration" "$staging_configuration/secrets" "$staging_configuration/deployment.env"
chmod 0750 "$staging_configuration" "$staging_configuration/secrets"
chmod 0640 "$staging_configuration/deployment.env"

[[ ! -e $target_configuration && ! -L $target_configuration ]] || { echo "target configuration directory appeared during installation" >&2; exit 1; }
verify_runtime_configuration_tree "$staging_configuration"
(
  unset LLMGATEWAY_POSTGRES_PASSWORD LLMGATEWAY_DATABASE_URL LLMGATEWAY_VALKEY_PASSWORD \
    LLMGATEWAY_MASTER_KEYS LLMGATEWAY_SESSION_PEPPER LLMGATEWAY_API_KEY_PEPPER \
    LLMGATEWAY_COORDINATION_KEY_HASH_SECRET
  load_llmgateway_environment "$staging_configuration/deployment.env"
  require_configuration_bindings "$target_configuration"
  export LLMGATEWAY_POSTGRES_PASSWORD_FILE="$staging_configuration/secrets/postgres-password"
  export LLMGATEWAY_DATABASE_URL_FILE="$staging_configuration/secrets/database-url"
  export LLMGATEWAY_VALKEY_PASSWORD_FILE="$staging_configuration/secrets/valkey-password"
  export LLMGATEWAY_VALKEY_ACL_FILE="$staging_configuration/secrets/valkey-acl"
  export LLMGATEWAY_MASTER_KEYS_FILE="$staging_configuration/secrets/master-keys"
  export LLMGATEWAY_SESSION_PEPPER_FILE="$staging_configuration/secrets/session-pepper"
  export LLMGATEWAY_API_KEY_PEPPER_FILE="$staging_configuration/secrets/api-key-pepper"
  export LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE="$staging_configuration/secrets/coordination-secret"
  require_file_secrets
  require_immutable_gateway_image
)
mv -T -- "$staging_configuration" "$target_configuration"
staging_configuration=
release_llmgateway_maintenance_lock
maintenance_lock_held=false
trap - EXIT INT TERM
echo "Restored configuration installed for database $target_database."
