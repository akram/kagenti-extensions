#!/usr/bin/env bash
# Allow the webhook namespace to pull images built in kagenti-images (internal registry).
# Without this, kubelet fails with: authentication required / ErrImagePull.
#
# Usage:
#   ./scripts/openshift/kagenti-images-grant-webhook-pull.sh
#
# Environment:
#   KAGENTI_IMAGES_NAMESPACE=kagenti-images
#   WEBHOOK_NAMESPACE=kagenti-webhook-system
set -euo pipefail

IMG_NS="${KAGENTI_IMAGES_NAMESPACE:-kagenti-images}"
WEBHOOK_NS="${WEBHOOK_NAMESPACE:-kagenti-webhook-system}"

if ! command -v oc &>/dev/null; then
  echo "error: oc not found" >&2
  exit 1
fi

echo "Granting system:image-puller in ${IMG_NS} to all service accounts in ${WEBHOOK_NS}..."
oc policy add-role-to-group system:image-puller "system:serviceaccounts:${WEBHOOK_NS}" -n "${IMG_NS}"

echo "Done. Restart the webhook deployment if it was already failing:"
echo "  oc rollout restart deployment/kagenti-webhook-controller-manager -n ${WEBHOOK_NS}"
