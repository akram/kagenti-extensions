#!/usr/bin/env bash
# Creates namespace kagenti-images, ImageStreams, and BuildConfigs for kagenti-extensions images.
# Requires: oc (logged in), envsubst (gettext package on macOS: brew install gettext && brew link gettext --force)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MANIFEST_DIR="${REPO_ROOT}/deploy/openshift/kagenti-images"

# Default: akram fork + feature branch (override for upstream/main or other forks).
export KAGENTI_EXTENSIONS_GIT_URI="${KAGENTI_EXTENSIONS_GIT_URI:-https://github.com/akram/kagenti-extensions.git}"
export KAGENTI_EXTENSIONS_GIT_REF="${KAGENTI_EXTENSIONS_GIT_REF:-feature/keycloak-admin-no-pod-secret}"

if ! command -v oc &>/dev/null; then
  echo "error: oc not found" >&2
  exit 1
fi
if ! command -v envsubst &>/dev/null; then
  echo "error: envsubst not found (install gettext)" >&2
  exit 1
fi

echo "Using Git ${KAGENTI_EXTENSIONS_GIT_URI} @ ${KAGENTI_EXTENSIONS_GIT_REF}"

oc apply -f "${MANIFEST_DIR}/namespace.yaml"
oc apply -f "${MANIFEST_DIR}/imagestreams.yaml"
envsubst < "${MANIFEST_DIR}/buildconfigs.yaml" | oc apply -f -

echo ""
echo "BuildConfigs applied. Start builds with:"
echo "  ${SCRIPT_DIR}/kagenti-images-start-builds.sh"
echo "Or individually: oc start-build kagenti-webhook -n kagenti-images --follow"
