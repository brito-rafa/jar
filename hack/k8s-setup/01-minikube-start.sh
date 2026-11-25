#!/usr/bin/env bash
# 01-minikube.sh — single-node minikube profile with the Radeon passed into the node.
[ -n "${BASH_VERSION:-}" ] || exec bash "$0" "$@"   # re-run under bash if started with sh/dash
set -euo pipefail

PROFILE="${PROFILE:-kserve}"   # separate minikube profile, so other clusters are left alone
CPUS="${CPUS:-10}"
MEMORY="${MEMORY:-32g}"
k() { kubectl --context "$PROFILE" "$@"; }

[[ -e /dev/kfd ]] || { echo "/dev/kfd not found: is the amdgpu driver loaded?" >&2; exit 1; }

[[ -e /dev/nvidia0 ]] || { echo "/dev/nvidia0 not found: is the nvidia driver loaded?" >&2; exit 1; }

# The docker driver is the only one minikube supports for AMD GPUs (minikube v1.35+).
# Agent Substrate (05-kagent.sh) needs the beta PodCertificateRequest API: its feature gate plus the
# certificates.k8s.io/v1beta1 resource, which Kubernetes does not serve by default.
minikube start -p "$PROFILE" --driver docker --container-runtime docker --gpus amd --gpus nvidia.com \
  --cpus "$CPUS" --memory "$MEMORY" \
  --feature-gates=PodCertificateRequest=true \
  --extra-config=apiserver.runtime-config=certificates.k8s.io/v1beta1/podcertificaterequests=true

# The model pods mount these device nodes directly, so confirm they made it into the node.
echo "SSHing to control plabe and showing devices - it must show /dev/kfd and render cards"
minikube -p "$PROFILE" ssh -- ls -l /dev/kfd /dev/dri /dev/nvidia0

minikube status -p "$PROFILE"

# An existing profile can keep its earlier API server flags, so confirm the API is really served.
if k get --raw /apis/certificates.k8s.io/v1beta1 2>/dev/null | grep -q '"podcertificaterequests"'; then
  echo "certificates.k8s.io/v1beta1 PodCertificateRequest: served"
else
  echo "WARNING: certificates.k8s.io/v1beta1 PodCertificateRequest is not served; 05-kagent.sh will stop." >&2
  echo "         If this profile existed before, recreate it: minikube delete -p $PROFILE, then rerun." >&2
fi

minikube profile "$PROFILE"

echo "Allocatable:"
k get node "$PROFILE" -o jsonpath='{.status.allocatable}{"\n"}'
