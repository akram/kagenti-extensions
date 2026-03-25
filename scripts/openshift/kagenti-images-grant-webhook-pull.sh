#!/usr/bin/env bash
# Allow namespaces to pull images built in kagenti-images (internal registry).
# Without this, kubelet fails with: authentication required / ErrImagePull.
#
# Usage:
#   ./scripts/openshift/kagenti-images-grant-webhook-pull.sh
#
# Environment:
#   KAGENTI_IMAGES_NAMESPACE=kagenti-images
#   WEBHOOK_NAMESPACE=kagenti-webhook-system
#   AGENT_NAMESPACES="team1 team2"   # optional; each agent ns that runs injected workloads using those images
set -euo pipefail

IMG_NS="${KAGENTI_IMAGES_NAMESPACE:-kagenti-images}"
WEBHOOK_NS="${WEBHOOK_NAMESPACE:-kagenti-webhook-system}"
AGENT_NAMESPACES="${AGENT_NAMESPACES:-}"

if ! command -v oc &>/dev/null; then
  echo "error: oc not found" >&2
  exit 1
fi

grant_ns() {
  local target_ns="$1"
  echo "Granting system:image-puller in ${IMG_NS} to all service accounts in ${target_ns}..."
  oc policy add-role-to-group system:image-puller "system:serviceaccounts:${target_ns}" -n "${IMG_NS}"
}

grant_ns "${WEBHOOK_NS}"

for ns in ${AGENT_NAMESPACES}; do
  [[ -z "${ns}" ]] && continue
  grant_ns "${ns}"
done

echo "Done. Restart workloads if they were already in ImagePullBackOff:"
echo "  oc rollout restart deployment/kagenti-webhook-controller-manager -n ${WEBHOOK_NS}"
echo "  # for each agent namespace: oc rollout restart deployment/<workload> -n <ns>"
