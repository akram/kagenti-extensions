#!/usr/bin/env bash
# Runs OpenShift builds for all kagenti-images BuildConfigs (sequential, --follow).
set -euo pipefail

NS="${KAGENTI_IMAGES_NAMESPACE:-kagenti-images}"

BUILDS=(
  kagenti-webhook
  kagenti-proxy-init
  kagenti-envoy-with-processor
  kagenti-authbridge
  kagenti-client-registration
)

for bc in "${BUILDS[@]}"; do
  echo "========== oc start-build ${bc} -n ${NS} --follow =========="
  oc start-build "${bc}" -n "${NS}" --follow
done

echo "All builds finished."
