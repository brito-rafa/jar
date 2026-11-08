#!/usr/bin/env bash
# 01-minikube.sh — single-node minikube profile with the Radeon passed into the node.
[ -n "${BASH_VERSION:-}" ] || exec bash "$0" "$@"   # re-run under bash if started with sh/dash
set -euo pipefail

PROFILE="${PROFILE:-kserve}"   # separate minikube profile, so other clusters are left alone
CPUS="${CPUS:-10}"
MEMORY="${MEMORY:-32g}"
k() { kubectl --context "$PROFILE" "$@"; }

[[ -e /dev/kfd ]] || { echo "/dev/kfd not found: is the amdgpu driver loaded?" >&2; exit 1; }

# The docker driver is the only one minikube supports for AMD GPUs (minikube v1.35+).
minikube start -p "$PROFILE" --driver docker --container-runtime docker --gpus amd \
  --cpus "$CPUS" --memory "$MEMORY"

# The model pods mount these device nodes directly, so confirm they made it into the node.
echo "SSHing to control plabe and showing devices - it must show /dev/kfd and render cards"
minikube -p "$PROFILE" ssh -- ls -l /dev/kfd /dev/dri

minikube status -p "$PROFILE"

minikube profile "$PROFILE"

echo "Allocatable:"
k get node "$PROFILE" -o jsonpath='{.status.allocatable}{"\n"}'
