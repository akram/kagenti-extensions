# OpenShift: build kagenti-extensions images in `kagenti-images`

This directory holds static manifests. The scripts under `scripts/openshift/` apply them and drive builds + Helm upgrades.

## One-time: allow the webhook namespace to pull images

If the webhook runs in another namespace (e.g. `kagenti-webhook-system`), grant pull access to ImageStreams in `kagenti-images`:

```bash
oc policy add-role-to-group system:image-puller system:serviceaccounts:kagenti-webhook-system -n kagenti-images
```

## Create namespace, ImageStreams, and BuildConfigs

From the **kagenti-extensions** repo root:

```bash
chmod +x scripts/openshift/kagenti-images-apply-buildconfigs.sh
./scripts/openshift/kagenti-images-apply-buildconfigs.sh
```

Defaults target **`https://github.com/akram/kagenti-extensions.git`** at branch **`feature/keycloak-admin-no-pod-secret`**. Override when needed:

```bash
export KAGENTI_EXTENSIONS_GIT_URI='https://github.com/kagenti/kagenti-extensions.git'
export KAGENTI_EXTENSIONS_GIT_REF='main'
./scripts/openshift/kagenti-images-apply-buildconfigs.sh
```

For a **private** fork or repo, set `KAGENTI_EXTENSIONS_GIT_URI`, create a source secret, and add `sourceSecret` to each `BuildConfig` (see [OpenShift: private Git repository](https://docs.openshift.com/container-platform/latest/cicd/builds/creating-build-inputs.html#builds-inputs-private-repositories_creating-build-inputs)).

## Build all images (OpenShift)

```bash
chmod +x scripts/openshift/kagenti-images-start-builds.sh
./scripts/openshift/kagenti-images-start-builds.sh
```

Or a single image:

```bash
oc start-build kagenti-webhook -n kagenti-images --follow
```

## Upgrade existing Helm installation (webhook + sidecar image defaults)

After builds succeed, point the chart at the internal registry and restart the deployment:

```bash
export HELM_RELEASE=kagenti-webhook          # your release name
export HELM_NAMESPACE=kagenti-webhook-system   # chart namespaceOverride default
# Optional: HELM_EXTRA_ARGS='--set registrar.existingSecret=kagenti-keycloak-registrar'

chmod +x scripts/openshift/kagenti-images-upgrade-webhook-helm.sh
./scripts/openshift/kagenti-images-upgrade-webhook-helm.sh
```

The script reads each ImageStream’s `status.dockerImageRepository`, runs `helm upgrade`, and `oc rollout restart` for the controller deployment.

### Manual Helm command (equivalent)

If you prefer not to use the script, after builds complete:

```bash
NS_IMG=kagenti-images
WEBHOOK_REPO=$(oc get is kagenti-webhook -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}')
TAG=latest
helm upgrade kagenti-webhook ./charts/kagenti-webhook \
  --namespace kagenti-webhook-system \
  --set image.repository="$WEBHOOK_REPO" \
  --set image.tag="$TAG" \
  --set defaults.images.envoyProxy="$(oc get is envoy-with-processor -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):$TAG" \
  --set defaults.images.proxyInit="$(oc get is proxy-init -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):$TAG" \
  --set defaults.images.authbridge="$(oc get is authbridge -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):$TAG" \
  --set defaults.images.clientRegistration="$(oc get is client-registration -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):$TAG"

oc rollout restart deployment -n kagenti-webhook-system \
  -l 'app.kubernetes.io/instance=kagenti-webhook,control-plane=controller-manager'
```

## BuildConfigs created

| BuildConfig | Context dir | Output ImageStream |
|-------------|-------------|-------------------|
| `kagenti-webhook` | `kagenti-webhook/` | `kagenti-webhook:latest` |
| `kagenti-proxy-init` | `AuthBridge/AuthProxy/` | `proxy-init:latest` |
| `kagenti-envoy-with-processor` | `AuthBridge/AuthProxy/` | `envoy-with-processor:latest` |
| `kagenti-authbridge` | `AuthBridge/` (Dockerfile `AuthProxy/Dockerfile.authbridge`) | `authbridge:latest` |
| `kagenti-client-registration` | `AuthBridge/client-registration/` | `client-registration:latest` |
