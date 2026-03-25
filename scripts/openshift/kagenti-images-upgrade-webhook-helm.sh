#!/usr/bin/env bash
# After images are built in kagenti-images, upgrade the Helm release to use ImageStream pull specs
# and roll the webhook deployment.
#
# Usage:
#   export HELM_RELEASE=kagenti-webhook
#   export HELM_NAMESPACE=kagenti-webhook-system
#   ./scripts/openshift/kagenti-images-upgrade-webhook-helm.sh
#
# Optional:
#   KAGENTI_IMAGES_NAMESPACE=kagenti-images
#   HELM_EXTRA_ARGS="--set registrar.existingSecret=my-registrar-secret"
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHART="${REPO_ROOT}/charts/kagenti-webhook"

IMG_NS="${KAGENTI_IMAGES_NAMESPACE:-kagenti-images}"
RELEASE="${HELM_RELEASE:-kagenti-webhook}"
NAMESPACE="${HELM_NAMESPACE:-kagenti-webhook-system}"

if ! command -v oc &>/dev/null || ! command -v helm &>/dev/null; then
  echo "error: oc and helm are required" >&2
  exit 1
fi

repo_for_is() {
  local name="$1"
  oc get is "${name}" -n "${IMG_NS}" -o jsonpath='{.status.dockerImageRepository}'
}

WEBHOOK_REPO="$(repo_for_is kagenti-webhook)"
ENVOY_REPO="$(repo_for_is envoy-with-processor)"
PROXY_INIT_REPO="$(repo_for_is proxy-init)"
AUTHBRIDGE_REPO="$(repo_for_is authbridge)"
CLIENT_REG_REPO="$(repo_for_is client-registration)"

if [[ -z "${WEBHOOK_REPO}" || -z "${ENVOY_REPO}" ]]; then
  echo "error: ImageStream status.dockerImageRepository is empty. Complete at least one build in ${IMG_NS}." >&2
  echo "Run: scripts/openshift/kagenti-images-start-builds.sh" >&2
  exit 1
fi

TAG="${IMAGE_TAG:-latest}"

echo "Helm upgrade ${RELEASE} in ${NAMESPACE}"
echo "  manager image:     ${WEBHOOK_REPO}:${TAG}"
echo "  envoy-with-proc:   ${ENVOY_REPO}:${TAG}"
echo "  proxy-init:        ${PROXY_INIT_REPO}:${TAG}"
echo "  authbridge:        ${AUTHBRIDGE_REPO}:${TAG}"
echo "  client-reg:        ${CLIENT_REG_REPO}:${TAG}"

helm upgrade "${RELEASE}" "${CHART}" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --set "image.repository=${WEBHOOK_REPO}" \
  --set "image.tag=${TAG}" \
  --set "image.pullPolicy=Always" \
  --set "defaults.images.envoyProxy=${ENVOY_REPO}:${TAG}" \
  --set "defaults.images.proxyInit=${PROXY_INIT_REPO}:${TAG}" \
  --set "defaults.images.authbridge=${AUTHBRIDGE_REPO}:${TAG}" \
  --set "defaults.images.clientRegistration=${CLIENT_REG_REPO}:${TAG}" \
  ${HELM_EXTRA_ARGS:-}

echo "Restarting webhook deployment (new image + refreshed platform defaults ConfigMap)..."
oc rollout restart deployment -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE},control-plane=controller-manager" || true
oc rollout status deployment -n "${NAMESPACE}" -l "app.kubernetes.io/instance=${RELEASE},control-plane=controller-manager" --timeout=180s || true
