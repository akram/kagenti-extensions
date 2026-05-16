.PHONY: docker-build-init load-image

KIND_CLUSTER_NAME ?= kagenti

# Build the proxy-init image (iptables init container, used by
# AuthBridge envoy-sidecar mode for transparent traffic interception).
docker-build-init:
	podman build -f Dockerfile.init -t proxy-init:latest .

# Load the locally-built proxy-init image into a Kind cluster and
# tag it under docker.io/library/ so Kubernetes can resolve the bare
# image name. (Podman tags as localhost/, but kubelet looks under
# docker.io/library/ for unqualified names.)
KIND_NODE ?= $(KIND_CLUSTER_NAME)-control-plane
load-image:
	kind load docker-image proxy-init:latest --name $(KIND_CLUSTER_NAME)
	podman exec $(KIND_NODE) ctr --namespace=k8s.io images tag \
	  localhost/proxy-init:latest docker.io/library/proxy-init:latest 2>/dev/null || true
