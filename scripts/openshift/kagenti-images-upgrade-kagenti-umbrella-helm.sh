#!/usr/bin/env bash
# After images are built in kagenti-images, upgrade the *umbrella* kagenti Helm release
# so the kagenti-webhook-chart subchart uses internal-registry ImageStreams for:
#   - manager (kagenti-webhook)
#   - defaults injected into workloads (envoy, proxy-init, authbridge, client-registration)
#
# Use this when the platform was installed with: helm install kagenti .../kagenti/charts/kagenti
# For a standalone webhook release only, use kagenti-images-upgrade-webhook-helm.sh instead.
#
# Usage (from kagenti-extensions repo root):
#   ./scripts/openshift/kagenti-images-upgrade-kagenti-umbrella-helm.sh
#
# Environment:
#   UMBRELLA_RELEASE=kagenti
#   UMBRELLA_NAMESPACE=kagenti-system
#   KAGENTI_CHART=/path/to/kagenti/charts/kagenti   # required if not ../kagenti/charts/kagenti
#   KAGENTI_IMAGES_NAMESPACE=kagenti-images
#   IMAGE_TAG=latest
#   WEBHOOK_DEPLOY_NAMESPACE=kagenti-webhook-system
#   HELM_EXTRA_ARGS='--set kagenti-webhook-chart.registrar.existingSecret=...'
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

IMG_NS="${KAGENTI_IMAGES_NAMESPACE:-kagenti-images}"
RELEASE="${UMBRELLA_RELEASE:-kagenti}"
NS="${UMBRELLA_NAMESPACE:-kagenti-system}"
TAG="${IMAGE_TAG:-latest}"
WEBHOOK_NS="${WEBHOOK_DEPLOY_NAMESPACE:-kagenti-webhook-system}"
PREFIX="kagenti-webhook-chart"

if [[ -n "${KAGENTI_CHART:-}" ]]; then
  CHART="${KAGENTI_CHART}"
elif [[ -d "${REPO_ROOT}/../kagenti/charts/kagenti" ]]; then
  CHART="$(cd "${REPO_ROOT}/../kagenti/charts/kagenti" && pwd)"
else
  echo "error: set KAGENTI_CHART to the umbrella chart directory (e.g. .../kagenti/charts/kagenti)" >&2
  exit 1
fi

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
  echo "error: ImageStream status.dockerImageRepository is empty in ${IMG_NS}." >&2
  exit 1
fi

echo "Helm upgrade ${RELEASE} in ${NS} (chart: ${CHART})"
echo "  ${PREFIX}.image:              ${WEBHOOK_REPO}:${TAG}"
echo "  ${PREFIX}.defaults envoy:      ${ENVOY_REPO}:${TAG}"
echo "  ${PREFIX}.defaults proxy-init: ${PROXY_INIT_REPO}:${TAG}"
echo "  ${PREFIX}.defaults authbridge: ${AUTHBRIDGE_REPO}:${TAG}"
echo "  ${PREFIX}.defaults client-reg: ${CLIENT_REG_REPO}:${TAG}"

helm upgrade "${RELEASE}" "${CHART}" \
  --namespace "${NS}" \
  --reuse-values \
  --set "${PREFIX}.image.repository=${WEBHOOK_REPO}" \
  --set "${PREFIX}.image.tag=${TAG}" \
  --set "${PREFIX}.image.pullPolicy=Always" \
  --set "${PREFIX}.defaults.images.envoyProxy=${ENVOY_REPO}:${TAG}" \
  --set "${PREFIX}.defaults.images.proxyInit=${PROXY_INIT_REPO}:${TAG}" \
  --set "${PREFIX}.defaults.images.authbridge=${AUTHBRIDGE_REPO}:${TAG}" \
  --set "${PREFIX}.defaults.images.clientRegistration=${CLIENT_REG_REPO}:${TAG}" \
  ${HELM_EXTRA_ARGS:-}

echo "Restarting webhook controller (reloads manager image + platform defaults ConfigMap)..."
oc rollout restart deployment/kagenti-webhook-controller-manager -n "${WEBHOOK_NS}" || true
oc rollout status deployment/kagenti-webhook-controller-manager -n "${WEBHOOK_NS}" --timeout=180s || true

echo ""
echo "New workloads will use the updated sidecar images. Existing injected pods keep old images until recreated (rollout restart their Deployments/StatefulSets)."
