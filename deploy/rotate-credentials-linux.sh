#!/usr/bin/env bash
set -euo pipefail

[[ $# -eq 2 && $2 == --confirm-key-rotation ]] || {
  echo "usage: $0 DEPLOYMENT_ENV --confirm-key-rotation" >&2
  exit 2
}
[[ $(id -u) -eq 0 ]] || { echo "credential rotation requires root" >&2; exit 1; }

SCRIPT_DIRECTORY=$(cd -- "$(dirname -- "$0")" && pwd)
# shellcheck source=lib.sh
source "$SCRIPT_DIRECTORY/lib.sh"
deployment_environment=$(realpath "$1")
load_llmgateway_environment "$deployment_environment"
require_file_secrets
require_immutable_gateway_image
export DEPLOY_DIRECTORY=${DEPLOY_DIRECTORY:-$SCRIPT_DIRECTORY}

acquire_llmgateway_maintenance_lock credential-rotation
trap release_llmgateway_maintenance_lock EXIT
deployment_compose --profile migration run --rm migrate \
  -action rotate-credentials -confirm-key-rotation
release_llmgateway_maintenance_lock
trap - EXIT
echo "Provider credentials were re-encrypted with the active master key."
