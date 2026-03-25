# OpenShift: build kagenti-extensions images in `kagenti-images`

This directory holds static manifests. The scripts under `scripts/openshift/` apply them and drive builds + Helm upgrades.

## One-time: allow the webhook namespace to pull images

If the webhook runs in another namespace (e.g. `kagenti-webhook-system`), grant pull access to ImageStreams in `kagenti-images`. **Skip this and pulls fail with `authentication required` / `ErrImagePull`.**

```bash
chmod +x scripts/openshift/kagenti-images-grant-webhook-pull.sh
./scripts/openshift/kagenti-images-grant-webhook-pull.sh
```

Or manually:

```bash
oc policy add-role-to-group system:image-puller system:serviceaccounts:kagenti-webhook-system -n kagenti-images
```

Then restart the webhook if it was already in `ImagePullBackOff`:

```bash
oc rollout restart deployment/kagenti-webhook-controller-manager -n kagenti-webhook-system
```

### Troubleshooting: `Failed to pull image` / `authentication required`

The internal registry URL `image-registry.openshift-image-registry.svc:5000/kagenti-images/...` still requires authorization. Run the **`system:image-puller`** grant above (in **`kagenti-images`**, referencing **`system:serviceaccounts:<webhook-namespace>`**). If your webhook runs in a different namespace, set `WEBHOOK_NAMESPACE` when running the script or adjust the `oc policy` command.

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

After builds succeed, point the chart at the internal registry and restart the deployment.

### Umbrella `kagenti` release (typical platform install)

If you installed the platform with `helm install kagenti .../kagenti/charts/kagenti` (release usually in `kagenti-system`), the webhook is the **`kagenti-webhook-chart` subchart**. Use:

```bash
# Expects ../kagenti/charts/kagenti relative to this repo, or set KAGENTI_CHART to that directory.
export UMBRELLA_RELEASE=kagenti
export UMBRELLA_NAMESPACE=kagenti-system
# Optional: HELM_EXTRA_ARGS='--set kagenti-webhook-chart.registrar.existingSecret=kagenti-keycloak-registrar'

chmod +x scripts/openshift/kagenti-images-upgrade-kagenti-umbrella-helm.sh
./scripts/openshift/kagenti-images-upgrade-kagenti-umbrella-helm.sh
```

This sets `kagenti-webhook-chart.image` and `kagenti-webhook-chart.defaults.images.*` to `image-registry.openshift-image-registry.svc:5000/kagenti-images/...:latest`, restarts `kagenti-webhook-controller-manager`, and uses `image.pullPolicy=Always` so nodes repull `:latest`.

**Already-running agent pods** keep their old sidecar images until you recreate them (e.g. `oc rollout restart deployment/<name> -n <agent-namespace>`).

### Standalone webhook release only

```bash
export HELM_RELEASE=kagenti-webhook          # your release name
export HELM_NAMESPACE=kagenti-webhook-system   # chart namespaceOverride default
# Optional: HELM_EXTRA_ARGS='--set registrar.existingSecret=kagenti-keycloak-registrar'

chmod +x scripts/openshift/kagenti-images-upgrade-webhook-helm.sh
./scripts/openshift/kagenti-images-upgrade-webhook-helm.sh
```

The scripts read each ImageStream’s `status.dockerImageRepository`, run `helm upgrade`, and restart the webhook deployment.

### Manual Helm command (umbrella `kagenti`)

```bash
NS_IMG=kagenti-images
TAG=latest
WEBHOOK_REPO=$(oc get is kagenti-webhook -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}')
P="kagenti-webhook-chart"
helm upgrade kagenti /path/to/kagenti/charts/kagenti \
  --namespace kagenti-system \
  --reuse-values \
  --set "${P}.image.repository=${WEBHOOK_REPO}" \
  --set "${P}.image.tag=${TAG}" \
  --set "${P}.image.pullPolicy=Always" \
  --set "${P}.defaults.images.envoyProxy=$(oc get is envoy-with-processor -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):${TAG}" \
  --set "${P}.defaults.images.proxyInit=$(oc get is proxy-init -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):${TAG}" \
  --set "${P}.defaults.images.authbridge=$(oc get is authbridge -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):${TAG}" \
  --set "${P}.defaults.images.clientRegistration=$(oc get is client-registration -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}'):${TAG}"

oc rollout restart deployment/kagenti-webhook-controller-manager -n kagenti-webhook-system
```

### Manual Helm command (standalone webhook)

Use `app.kubernetes.io/instance=<your-helm-release-name>` in the rollout selector.

```bash
NS_IMG=kagenti-images
WEBHOOK_REPO=$(oc get is kagenti-webhook -n "$NS_IMG" -o jsonpath='{.status.dockerImageRepository}')
TAG=latest
helm upgrade kagenti-webhook ./charts/kagenti-webhook \
  --namespace kagenti-webhook-system \
  --set image.repository="$WEBHOOK_REPO" \
  --set image.tag="$TAG" \
  --set image.pullPolicy=Always \
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
