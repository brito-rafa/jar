#!/usr/bin/env bash
# 04-tailscale.sh — Tailscale Kubernetes Operator, which gives Services annotated with
# tailscale.com/expose (JAR's jarchat Service) their own tailnet hostname.
#
# Follows https://tailscale.com/docs/kubernetes-operator/install-operator (Helm). First, in the
# Tailscale admin console:
#   1. Access controls, under tagOwners:  "tag:k8s-operator": [],  "tag:k8s": ["tag:k8s-operator"],
#   2. Trust credentials: an OAuth client with write scope for General/Services, Devices/Core and
#      Keys/Auth Keys, each with tag:k8s-operator. Keep its client ID and secret.
# Your access rules must also let your devices reach tag:k8s (port 80 for jarchat); the default
# allow-all policy does.
#
# Needs kubectl and helm; uses the current kubectl context. The OAuth client ID and secret come
# from TS_OAUTH_CLIENT_ID / TS_OAUTH_CLIENT_SECRET or a prompt (the secret is not echoed) and reach
# Kubernetes only on stdin, never in a command line or Helm values. Re-runnable: an existing
# operator-oauth Secret is kept unless new credentials are given.
[ -n "${BASH_VERSION:-}" ] || exec bash "$0" "$@"   # re-run under bash if started with sh/dash
set +o posix   # macOS sh is bash in POSIX mode; parse the rest as normal bash
set -euo pipefail

TS_OPERATOR_VERSION="${TS_OPERATOR_VERSION:-1.104.7}"   # tailscale-operator chart (2026-11-12)
NS="${TS_NAMESPACE:-tailscale}"
RELEASE=tailscale-operator
# Chart defaults: the operator Deployment is "operator" and reads the "operator-oauth" Secret.
DEPLOYMENT=operator
SECRET=operator-oauth
# Take the credentials into shell variables before running any command, so the secret is never
# in the environment of kubectl, helm, or anything else this script starts.
client_id="${TS_OAUTH_CLIENT_ID:-}"
client_secret="${TS_OAUTH_CLIENT_SECRET:-}"
unset TS_OAUTH_CLIENT_ID TS_OAUTH_CLIENT_SECRET

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing required tool: $1" >&2; exit 1; }; }
for tool in kubectl helm; do need "$tool"; done
kubectl cluster-info >/dev/null 2>&1 || { echo "Cannot reach the cluster in the current kubectl context." >&2; exit 1; }
# Values go into YAML double quotes, so they must not contain quotes, backslashes, or spaces.
valid() { [ -n "$1" ] && [[ "$1" != *[[:space:]\"\\]* ]]; }

echo "==> Namespace ${NS}"
kubectl create namespace "$NS" --dry-run=client -o yaml | kubectl apply -f - >/dev/null

updated_secret=false
if [ -z "$client_id$client_secret" ] && kubectl get secret "$SECRET" -n "$NS" >/dev/null 2>&1; then
  echo "==> Keeping the existing ${SECRET} Secret (set TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET to replace it)"
else
  if [ -z "$client_id" ] || [ -z "$client_secret" ]; then
    [ -t 0 ] || { echo "Set TS_OAUTH_CLIENT_ID and TS_OAUTH_CLIENT_SECRET, or run this in a terminal." >&2; exit 1; }
    [ -n "$client_id" ] || read -rp 'Tailscale OAuth client ID: ' client_id
    if [ -z "$client_secret" ]; then read -rsp 'Tailscale OAuth client secret: ' client_secret; echo; fi
  fi
  valid "$client_id" && valid "$client_secret" || {
    echo "The OAuth client ID and secret must be non-empty, without quotes, backslashes, or spaces." >&2; exit 1; }
  echo "==> ${SECRET} Secret"
  # printf is a shell builtin, so the secret never appears in a process's arguments.
  printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\nstringData:\n  client_id: "%s"\n  client_secret: "%s"\n' \
    "$SECRET" "$NS" "$client_id" "$client_secret" \
    | kubectl apply --server-side --force-conflicts --field-manager=jar-tailscale-setup -f - >/dev/null
  updated_secret=true
fi
unset client_secret

echo "==> Tailscale operator chart ${TS_OPERATOR_VERSION}"
already_installed=false
helm status "$RELEASE" -n "$NS" >/dev/null 2>&1 && already_installed=true
helm repo add tailscale https://pkgs.tailscale.com/helmcharts --force-update >/dev/null
helm repo update tailscale >/dev/null
# No oauth.* values: the chart then mounts the operator-oauth Secret created above.
helm upgrade --install "$RELEASE" tailscale/tailscale-operator --version "$TS_OPERATOR_VERSION" \
  --namespace "$NS" --wait --timeout 5m
# The operator reads its credentials at startup; restart it if they changed on a re-run.
if [ "$already_installed" = true ] && [ "$updated_secret" = true ]; then
  kubectl -n "$NS" rollout restart "deployment/${DEPLOYMENT}" >/dev/null
fi
kubectl -n "$NS" rollout status "deployment/${DEPLOYMENT}" --timeout=5m
kubectl -n "$NS" get pods

cat <<EOF

The Tailscale operator is running in namespace ${NS}. It should appear as "tailscale-operator"
(tag:k8s-operator) in the admin console's Machines page.
  kubectl -n ${NS} logs deploy/${DEPLOYMENT} --tail=20   # OAuth scope or tag errors show here
JAR's jarchat Service carries tailscale.com/expose, so after make deploy the operator adds a ts-*
proxy Pod here and a "jarchat" machine: open http://jarchat, or set CHAT_PUBLIC_URL to the full
MagicDNS name and run make deploy again.
EOF
