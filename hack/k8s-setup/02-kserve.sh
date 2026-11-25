#!/usr/bin/env bash
# 02-kserve.sh — cert-manager and KServe (Standard deployment mode, no Istio), then the example
# model stack from config/manifests/k8s/kserve:
#   example-cluster-serving-runtimes-cuda-and-rocm.yaml  hf-gguf downloader, llamacpp-cuda, llamacpp-rocm
#   example-isvc-ministral.yaml                          ministral-3-8b chat model (nvidia.com/gpu)
#   example-isvc-qwen3-8B-embedding.yaml                 qwen3-embedding-8b, 4096 dims (amd.com/gpu)
# Both predictor Services carry Tailscale operator annotations (tailnet names ministral and qwen);
# they appear on the tailnet once 04-tailscale.sh has installed the operator.
# Review the GPU resources and runtime images in those files first. Uses the current kubectl
# context. Re-runnable: Helm releases are upgraded in place and the manifests re-applied.
[ -n "${BASH_VERSION:-}" ] || exec bash "$0" "$@"   # re-run under bash if started with sh/dash
set +o posix   # macOS sh is bash in POSIX mode; parse the rest as normal bash
set -euo pipefail

CERT_MANAGER_VERSION=v1.21.3
KSERVE_VERSION=v0.20.0
NS="${NAMESPACE:-jar}"                 # JAR's namespace; the models live next to it
MODEL_TIMEOUT="${MODEL_TIMEOUT:-30m}"  # the first start downloads the GGUF files
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
EXAMPLES="$ROOT/config/manifests/k8s/kserve"
RUNTIMES="$EXAMPLES/example-cluster-serving-runtimes-cuda-and-rocm.yaml"
CHAT_MODEL_FILE="$EXAMPLES/example-isvc-ministral.yaml"
EMBEDDING_MODEL_FILE="$EXAMPLES/example-isvc-qwen3-8B-embedding.yaml"
CHAT_ISVC=ministral-3-8b
EMBEDDING_ISVC=qwen3-embedding-8b

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing required tool: $1" >&2; exit 1; }; }
for tool in kubectl helm; do need "$tool"; done
for file in "$RUNTIMES" "$CHAT_MODEL_FILE" "$EMBEDDING_MODEL_FILE"; do
  [ -f "$file" ] || { echo "Missing example manifest: $file" >&2; exit 1; }
done

echo "==> cert-manager ${CERT_MANAGER_VERSION}"
helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version "$CERT_MANAGER_VERSION" --namespace cert-manager --create-namespace \
  --set crds.enabled=true --wait

echo "==> KServe ${KSERVE_VERSION} CRDs"
helm upgrade --install kserve-crd oci://ghcr.io/kserve/charts/kserve-crd \
  --version "$KSERVE_VERSION" --namespace kserve --create-namespace --wait
kubectl wait --for=condition=Established --timeout=60s \
  crd/clusterstoragecontainers.serving.kserve.io \
  crd/clusterservingruntimes.serving.kserve.io \
  crd/inferenceservices.serving.kserve.io \
  crd/servingruntimes.serving.kserve.io

echo "==> KServe ${KSERVE_VERSION} controller (no Istio)"
helm upgrade --install kserve-resources oci://ghcr.io/kserve/charts/kserve-resources \
  --version "$KSERVE_VERSION" --namespace kserve --reuse-values \
  --set kserve.controller.gateway.disableIstioVirtualHost=true \
  --set kserve.controller.gateway.disableIngressCreation=true --wait
kubectl -n kserve get pods

echo "==> Runtimes: hf-gguf downloader, llamacpp-cuda, llamacpp-rocm (cluster-scoped)"
kubectl apply -f "$RUNTIMES"

# Each model requests one whole GPU; without the resource its predictor Pod stays Pending.
allocatable="$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.allocatable}{"\n"}{end}')"
for gpu in nvidia.com/gpu amd.com/gpu; do
  case "$allocatable" in
    *"\"$gpu\""*) ;;
    *) echo "WARNING: no node advertises $gpu; the model requesting it will stay Pending." >&2 ;;
  esac
done

echo "==> InferenceServices in namespace ${NS}"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$NS" apply -f "$CHAT_MODEL_FILE"
kubectl -n "$NS" apply -f "$EMBEDDING_MODEL_FILE"
echo "    Waiting up to ${MODEL_TIMEOUT} per model; the first start downloads the GGUF files."
kubectl -n "$NS" wait --for=condition=Ready "inferenceservice/${CHAT_ISVC}" --timeout="$MODEL_TIMEOUT"
kubectl -n "$NS" wait --for=condition=Ready "inferenceservice/${EMBEDDING_ISVC}" --timeout="$MODEL_TIMEOUT"

# KServe v0.20 copies predictor annotations only when it creates the predictor Service, so carry the
# Tailscale ones over to a Service created before they were added. Deleting them from the YAML does
# not remove them from the Service; use: kubectl annotate service <name> tailscale.com/expose-
TAILNET=""
sync_tailscale() {
  local isvc="$1" svc="$1-predictor" expose hostname
  expose="$(kubectl -n "$NS" get inferenceservice "$isvc" -o jsonpath='{.spec.predictor.annotations.tailscale\.com/expose}')"
  [ "$expose" = "true" ] || return 0
  hostname="$(kubectl -n "$NS" get inferenceservice "$isvc" -o jsonpath='{.spec.predictor.annotations.tailscale\.com/hostname}')"
  kubectl -n "$NS" annotate --overwrite service "$svc" tailscale.com/expose=true ${hostname:+"tailscale.com/hostname=$hostname"}
  TAILNET+="  http://${hostname:-$NS-$svc} -> $svc"$'\n'
}
sync_tailscale "$CHAT_ISVC"
sync_tailscale "$EMBEDDING_ISVC"
kubectl -n "$NS" get inferenceservices

cat <<EOF

Both models are ready. Settings for .env (JAR Pods reach the predictors through cluster DNS):
  CHAT_URL=http://${CHAT_ISVC}-predictor.${NS}.svc.cluster.local
  CHAT_API_MODEL=${CHAT_ISVC}
  EMBEDDING_URL=http://${EMBEDDING_ISVC}-predictor.${NS}.svc.cluster.local
  EMBEDDING_API_MODEL=${EMBEDDING_ISVC}
  EMBEDDING_MODEL=Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M
  DIMENSIONS=4096
EMBEDDING_MODEL and DIMENSIONS must match the index; changing them requires reindexing.
EOF
if [ -n "$TAILNET" ]; then
  printf '\nOn the tailnet, once the Tailscale operator runs (use these from clients outside the cluster):\n%s' "$TAILNET"
fi
