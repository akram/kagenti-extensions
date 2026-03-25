#!/usr/bin/env bash
# Copy keycloak-admin-secret from a namespace where Kagenti created it (e.g. team1, team2)
# into kagenti-webhook-system so you can run:
#   oc set env deployment/kagenti-webhook-controller-manager -n kagenti-webhook-system \
#     --from=secret/keycloak-admin-secret --containers=manager
#
# Usage:
#   ./kagenti-webhook-sync-keycloak-admin-secret.sh team1
#   ./kagenti-webhook-sync-keycloak-admin-secret.sh team1 my-custom-secret-name
#
# Environment:
#   DEST_NS=kagenti-webhook-system
set -euo pipefail

SRC_NS="${1:?usage: $0 <source-namespace> [secret-name]}"
SECRET_NAME="${2:-keycloak-admin-secret}"
DEST_NS="${DEST_NS:-kagenti-webhook-system}"

if ! oc get secret "$SECRET_NAME" -n "$SRC_NS" &>/dev/null; then
  echo "error: secret ${SECRET_NAME} not found in namespace ${SRC_NS}" >&2
  echo "List candidates: oc get secrets -A | grep -E 'keycloak|admin'" >&2
  exit 1
fi

export DEST_NS
oc get secret "$SECRET_NAME" -n "$SRC_NS" -o json | python3 -c "
import json, sys, os
d = json.load(sys.stdin)
m = d['metadata']
for k in ('resourceVersion', 'uid', 'creationTimestamp', 'managedFields', 'ownerReferences'):
    m.pop(k, None)
m['namespace'] = os.environ['DEST_NS']
print(json.dumps(d))
" | oc apply -f -

echo "Applied ${SECRET_NAME} to namespace ${DEST_NS}. Next:"
echo "  oc set env deployment/kagenti-webhook-controller-manager -n ${DEST_NS} \\"
echo "    --from=secret/${SECRET_NAME} --containers=manager"
