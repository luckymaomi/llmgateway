#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 1 ]] || { echo "usage: install-linux.sh /path/to/deployment.env" >&2; exit 2; }
[[ $(id -u) -eq 0 ]] || { echo "installation requires root" >&2; exit 1; }

SOURCE_DIRECTORY=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$SOURCE_DIRECTORY/lib.sh"
environment_source=$(readlink -f "$1")
load_llmgateway_environment "$environment_source"
require_file_secrets
require_immutable_gateway_image

DEPLOY_DIRECTORY=/opt/llmgateway/deploy
install -d -m 0750 /opt/llmgateway "$DEPLOY_DIRECTORY" /etc/llmgateway
install -m 0644 "$SOURCE_DIRECTORY/compose.production.yaml" "$DEPLOY_DIRECTORY/compose.production.yaml"
install -m 0644 "$SOURCE_DIRECTORY/Caddyfile" "$DEPLOY_DIRECTORY/Caddyfile"
install -m 0644 "$SOURCE_DIRECTORY/lib.sh" "$DEPLOY_DIRECTORY/lib.sh"
install -m 0755 "$SOURCE_DIRECTORY/upgrade-linux.sh" "$DEPLOY_DIRECTORY/upgrade-linux.sh"
install -m 0640 "$environment_source" /etc/llmgateway/deployment.env

gateway_secret_files=(
  "$LLMGATEWAY_DATABASE_URL_FILE"
  "$LLMGATEWAY_VALKEY_PASSWORD_FILE"
  "$LLMGATEWAY_MASTER_KEYS_FILE"
  "$LLMGATEWAY_SESSION_PEPPER_FILE"
  "$LLMGATEWAY_API_KEY_PEPPER_FILE"
  "$LLMGATEWAY_COORDINATION_KEY_HASH_SECRET_FILE"
)
chown 65532:65532 "${gateway_secret_files[@]}"
chmod 0400 "${gateway_secret_files[@]}"
chown 999:1000 "$LLMGATEWAY_VALKEY_ACL_FILE"
chmod 0400 "$LLMGATEWAY_VALKEY_ACL_FILE"
chown root:root "$LLMGATEWAY_POSTGRES_PASSWORD_FILE"
chmod 0400 "$LLMGATEWAY_POSTGRES_PASSWORD_FILE"

export DEPLOY_DIRECTORY
deployment_compose config --quiet
deployment_compose pull
deployment_compose up --detach --wait postgres valkey
deployment_compose --profile migration run --rm migrate

install -m 0644 "$SOURCE_DIRECTORY/llmgateway-compose.service" /etc/systemd/system/llmgateway-compose.service
systemctl daemon-reload
systemctl enable --now llmgateway-compose.service
systemctl is-active --quiet llmgateway-compose.service
deployment_compose ps
echo "LLMGateway Linux production stack installed."
