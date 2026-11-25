#!/usr/bin/env bash
# 05-kagent.sh — kagent 1.0 and Agent Substrate, with PostgreSQL as a Deployment in the kagent
# namespace (config/manifests/k8s/kagent/example-postgres.yaml) instead of an external database.
#
# Follows "Install with Helm" in https://kagent.dev/docs/kagent/latest/setup/installation.
# Needs Kubernetes 1.37+ and kubectl, helm, jq, openssl, and curl. Installs the kubectl-ate plugin
# matching SUBSTRATE_VERSION into KUBECTL_ATE_DIR (default ~/.local/bin) when missing. Uses the current
# kubectl context. Re-runnable: existing pools, Secrets, and database logins are kept.
# Generated passwords travel only over stdin into Kubernetes Secrets; nothing secret is printed.
# Afterwards apply config/manifests/k8s/kagent/example-librarian.yaml (see SETUP.md).
[ -n "${BASH_VERSION:-}" ] || exec bash "$0" "$@"   # re-run under bash if started with sh/dash
set +o posix   # macOS sh is bash in POSIX mode; parse the rest as normal bash
set -euo pipefail

# Pinned together: the setup.sql digests below belong to these releases.
KAGENT_VERSION=1.0.0
SUBSTRATE_VERSION=1.0.0
KAGENT_SQL_URL="https://raw.githubusercontent.com/kagent-dev/kagent/refs/tags/v${KAGENT_VERSION}/go/core/pkg/postgressetup/setup.sql"
KAGENT_SQL_SHA256=ed02e38bd6ae05c6ac9b374610d3728bdb9c8749e238e358b92a146cd1184f59
SUBSTRATE_SQL_URL="https://raw.githubusercontent.com/kagent-dev/substrate/refs/tags/v${SUBSTRATE_VERSION}/pkg/postgressetup/setup.sql"
SUBSTRATE_SQL_SHA256=609aa9900dcee152a9e8cc75b9fd6ec57f37e70ed9dfd73c820cf2e2bf310a09

NS=kagent
ATE_NS=ate-system
PODCERT_NS=podcertificate-controller-system
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PG_MANIFEST="$ROOT/config/manifests/k8s/kagent/example-postgres.yaml"
PG_URL_BASE="kagent-postgres.${NS}.svc.cluster.local:5432/kagent"
PG_URL_OPTIONS="sslmode=disable"   # the example database has no TLS; see example-postgres.yaml
# Agent Substrate's API server issuer mode: auto, in-cluster, or external (see the kagent docs).
ISSUER_MODE="${ISSUER_MODE:-auto}"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing required tool: $1" >&2; exit 1; }; }
for tool in kubectl helm jq openssl curl; do need "$tool"; done
# The Substrate charts use certificates.k8s.io/v1beta1 PodCertificateRequest. Beta APIs are off by
# default, so check that exact version rather than any served version of the resource.
kubectl get --raw /apis/certificates.k8s.io/v1beta1 2>/dev/null \
  | jq -e '.resources[] | select(.name == "podcertificaterequests")' >/dev/null || {
  cat >&2 <<'EOF'
The cluster does not serve certificates.k8s.io/v1beta1 PodCertificateRequest, which Agent Substrate needs.
Enable it on the API server, for example on minikube:
  minikube start -p <profile> --feature-gates=PodCertificateRequest=true \
    --extra-config=apiserver.runtime-config=certificates.k8s.io/v1beta1/podcertificaterequests=true
Check with: kubectl get --raw /apis/certificates.k8s.io/v1beta1 | jq -r '.resources[].name'
EOF
  exit 1; }

exists() { kubectl get "$@" >/dev/null 2>&1; }
ensure_namespace() { kubectl create namespace "$1" --dry-run=client -o yaml | kubectl apply -f - >/dev/null; }
# apply_secret NAME NAMESPACE KEY VALUE [KEY VALUE ...]: values reach kubectl on stdin, never argv.
# Server-side apply keeps them out of the last-applied-configuration annotation that
# `kubectl describe` prints. Values must not contain double quotes or backslashes.
apply_secret() {
  local name=$1 namespace=$2
  shift 2
  {
    printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\nstringData:\n' "$name" "$namespace"
    while [ $# -gt 0 ]; do printf '  %s: "%s"\n' "$1" "$2"; shift 2; done
  } | kubectl apply --server-side --force-conflicts --field-manager=jar-kagent-setup -f - >/dev/null
}
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }
fetch_verified() { # URL SHA256 OUTPUT
  curl -fsSL -o "$3" "$1"
  [ "$(sha256 "$3")" = "$2" ] || { echo "Checksum mismatch for $1" >&2; exit 1; }
}
# kubectl-ate comes from the same release as the Substrate charts. When `kubectl ate` is not
# available, download that release's build for this OS/CPU, check it against the published
# SHA-256, and install it into KUBECTL_ATE_DIR. INSTALL_KUBECTL_ATE=false only checks.
KUBECTL_ATE_DIR="${KUBECTL_ATE_DIR:-$HOME/.local/bin}"
ensure_kubectl_ate() {
  local on_path=true os arch asset release
  case ":$PATH:" in *":$KUBECTL_ATE_DIR:"*) ;; *) on_path=false; PATH="$KUBECTL_ATE_DIR:$PATH" ;; esac
  kubectl ate --help >/dev/null 2>&1 && return 0
  release="https://github.com/kagent-dev/substrate/releases/download/v${SUBSTRATE_VERSION}"
  if [ "${INSTALL_KUBECTL_ATE:-true}" != true ]; then
    echo "Missing the kubectl-ate plugin: https://github.com/kagent-dev/substrate/releases/tag/v${SUBSTRATE_VERSION}" >&2
    exit 1
  fi
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$os" in darwin|linux) ;; *) echo "No kubectl-ate build for OS: $os" >&2; exit 1 ;; esac
  case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "No kubectl-ate build for CPU: $(uname -m)" >&2; exit 1 ;;
  esac
  asset="kubectl-ate-${os}-${arch}"
  echo "==> Installing kubectl-ate v${SUBSTRATE_VERSION} (${os}/${arch}) into ${KUBECTL_ATE_DIR}"
  curl -fsSL -o "$WORK/$asset" "$release/$asset"
  curl -fsSL -o "$WORK/$asset.sha256" "$release/$asset.sha256"
  [ "$(sha256 "$WORK/$asset")" = "$(cut -d' ' -f1 < "$WORK/$asset.sha256")" ] || {
    echo "Checksum mismatch for $asset; not installing it" >&2; exit 1; }
  mkdir -p "$KUBECTL_ATE_DIR"
  install -m 0755 "$WORK/$asset" "$KUBECTL_ATE_DIR/kubectl-ate"
  if ! kubectl ate --help >/dev/null 2>&1; then
    echo "kubectl-ate was installed but does not run:" >&2
    "$KUBECTL_ATE_DIR/kubectl-ate" --help >&2 || true
    exit 1
  fi
  if [ "$on_path" = false ]; then
    echo "    To use 'kubectl ate' in new shells, add $KUBECTL_ATE_DIR to PATH in your shell profile."
  fi
}
ensure_kubectl_ate
# Runs a setup script inside the database pod as the local superuser; stdin carries the
# password variables followed by the pinned SQL, in one transaction. Error context is
# suppressed because it could echo an ALTER ROLE ... PASSWORD statement.
psql_setup() {
  kubectl -n "$NS" exec -i deploy/kagent-postgres -c postgres -- \
    psql --no-psqlrc --quiet -U postgres -d kagent --set=ON_ERROR_STOP=1 --set=VERBOSITY=terse \
    --set=SHOW_CONTEXT=never --single-transaction "$@" --file=- >/dev/null
}

echo "==> CRDs: kagent ${KAGENT_VERSION}, Agent Substrate ${SUBSTRATE_VERSION}"
helm upgrade --install kagent-crds oci://ghcr.io/kagent-dev/kagent/helm/kagent-crds \
  --version "$KAGENT_VERSION" --namespace "$NS" --create-namespace --wait
helm upgrade --install substrate-crds oci://ghcr.io/kagent-dev/substrate/helm/substrate-crds \
  --version "$SUBSTRATE_VERSION" --namespace "$ATE_NS" --create-namespace --wait

echo "==> Identity pools"
ensure_namespace "$PODCERT_NS"
for pool in service-dns-ca-pool postgres-ca-pool pod-identity-ca-pool; do
  exists secret "$pool" -n "$PODCERT_NS" || kubectl ate admin make-ca-pool --ca-id=1 --key-type=ED25519 \
    --name="$pool" --secret-namespace="$PODCERT_NS"
done
exists secret actor-id-jwt-pool -n "$ATE_NS" || kubectl ate admin make-jwt-pool --key-id=1 \
  --name=actor-id-jwt-pool --secret-namespace="$ATE_NS"
exists secret actor-id-ca-pool -n "$ATE_NS" || kubectl ate admin make-ca-pool --ca-id=1 --key-type=ED25519 \
  --name=actor-id-ca-pool --secret-namespace="$ATE_NS"
exists secret egress-mitm-ca-pool -n "$ATE_NS" || kubectl ate admin make-ca-pool --ca-id=1 --key-type=ECDSAP256 \
  --name=egress-mitm-ca-pool --secret-namespace="$ATE_NS"
if ! exists secret actor-id-ca-certs -n "$ATE_NS"; then
  # The actor identity root certificate is public material.
  root_ca="$(kubectl get secret actor-id-ca-pool -n "$ATE_NS" -o jsonpath='{.data.pool}' | base64 --decode \
    | jq -r '.CAs[0].RootCertificateDER' | base64 --decode | openssl x509 -inform der -outform pem)"
  kubectl create secret generic actor-id-ca-certs -n "$ATE_NS" --from-literal=ca.crt="$root_ca"
fi

if ! exists configmap ate-api-authentication -n "$ATE_NS"; then
  issuer="$(kubectl get --raw /.well-known/openid-configuration | jq -r .issuer)"
  mode="$ISSUER_MODE"
  if [ "$mode" = auto ]; then
    case "$issuer" in
      https://kubernetes.default.svc*|*.svc|*.svc.*|*cluster.local*) mode=in-cluster ;;
      *) mode=external ;;
    esac
  fi
  echo "    token issuer ${issuer} (${mode})"
  auth="jwtProviders:
- name: kubernetes
  issuer: ${issuer}
  audiences: [api.ate-system.svc]
"
  if [ "$mode" = in-cluster ]; then
    auth+="  certificateAuthorityFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
  discoveryTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
"
  fi
  kubectl create configmap ate-api-authentication -n "$ATE_NS" --from-literal=authentication.yaml="$auth"
fi

echo "==> PodCertificate controller"
helm upgrade --install substrate-podcert oci://ghcr.io/kagent-dev/substrate/helm/substrate-podcert \
  --version "$SUBSTRATE_VERSION" --namespace "$PODCERT_NS" --create-namespace --wait --timeout 5m

echo "==> PostgreSQL in namespace ${NS}"
ensure_namespace "$NS"
exists secret kagent-postgres-admin -n "$NS" || apply_secret kagent-postgres-admin "$NS" POSTGRES_PASSWORD "$(openssl rand -hex 24)"
kubectl -n "$NS" apply -f "$PG_MANIFEST"
kubectl -n "$NS" rollout status deployment/kagent-postgres --timeout=5m

connection_secrets_ready=true
for secret in "$NS/kagent-postgres-owner" "$NS/kagent-postgres-readwrite" \
              "$ATE_NS/substrate-postgres-owner" "$ATE_NS/substrate-postgres-readwrite"; do
  exists secret "${secret#*/}" -n "${secret%%/*}" || connection_secrets_ready=false
done
if [ "$connection_secrets_ready" = false ]; then
  echo "==> Database roles, schemas, and connection Secrets"
  fetch_verified "$KAGENT_SQL_URL" "$KAGENT_SQL_SHA256" "$WORK/kagent-setup.sql"
  fetch_verified "$SUBSTRATE_SQL_URL" "$SUBSTRATE_SQL_SHA256" "$WORK/substrate-setup.sql"
  kagent_owner="$(openssl rand -hex 24)"
  kagent_readwrite="$(openssl rand -hex 24)"
  substrate_owner="$(openssl rand -hex 24)"
  substrate_readwrite="$(openssl rand -hex 24)"
  {
    printf '\\set kagent_owner_password %s\n\\set kagent_readwrite_password %s\n' "$kagent_owner" "$kagent_readwrite"
    cat "$WORK/kagent-setup.sql"
  } | psql_setup --set=kagent_schema=kagent \
      --set=kagent_owner_role=kagent_owner --set=kagent_owner_user=kagent_owner_user \
      --set=kagent_readwrite_role=kagent_readwrite --set=kagent_readwrite_user=kagent_readwrite_user
  {
    printf '\\set substrate_owner_password %s\n\\set substrate_readwrite_password %s\n' "$substrate_owner" "$substrate_readwrite"
    cat "$WORK/substrate-setup.sql"
  } | psql_setup --set=substrate_schema=substrate \
      --set=substrate_owner_role=substrate_owner --set=substrate_owner_user=substrate_owner_user \
      --set=substrate_readwrite_role=substrate_readwrite --set=substrate_readwrite_user=substrate_readwrite_user
  # Connection strings name the login user only; each component assumes its role after connecting.
  apply_secret kagent-postgres-owner "$NS" ownerConnectionString \
    "postgresql://kagent_owner_user:${kagent_owner}@${PG_URL_BASE}?${PG_URL_OPTIONS}"
  apply_secret kagent-postgres-readwrite "$NS" readWriteConnectionString \
    "postgresql://kagent_readwrite_user:${kagent_readwrite}@${PG_URL_BASE}?${PG_URL_OPTIONS}"
  apply_secret substrate-postgres-owner "$ATE_NS" ownerConnectionString \
    "postgresql://substrate_owner_user:${substrate_owner}@${PG_URL_BASE}?${PG_URL_OPTIONS}"
  apply_secret substrate-postgres-readwrite "$ATE_NS" readWriteConnectionString \
    "postgresql://substrate_readwrite_user:${substrate_readwrite}@${PG_URL_BASE}?${PG_URL_OPTIONS}"
  unset kagent_owner kagent_readwrite substrate_owner substrate_readwrite
  # Components read connection Secrets at startup; restart any installed by an earlier run.
  if helm status substrate -n "$ATE_NS" >/dev/null 2>&1; then
    kubectl -n "$ATE_NS" rollout restart deployment >/dev/null
  fi
  if helm status kagent -n "$NS" >/dev/null 2>&1; then
    kubectl -n "$NS" rollout restart deployment/kagent-controller >/dev/null
  fi
fi

echo "==> Agent Substrate"
# Defaults of the substrate chart's bundled object store (rustfs), as in the kagent docs.
exists secret kagent-rustfs-credentials -n "$ATE_NS" || kubectl create secret generic kagent-rustfs-credentials \
  -n "$ATE_NS" --from-literal=AWS_ACCESS_KEY_ID=rustfsadmin --from-literal=AWS_SECRET_ACCESS_KEY=rustfsadmin
cat > "$WORK/substrate-atelet-values.yaml" <<'EOF'
preview:
  - PerTemplateSnapshotLocation
  - GoldenEgressPolicy
objectStore:
  backend: s3
  endpoint: http://rustfs.ate-system.svc:9000
  region: us-east-1
  pathStyle: true
  credentialsSecret: kagent-rustfs-credentials
EOF
helm upgrade --install substrate-atelet oci://ghcr.io/kagent-dev/substrate/helm/substrate-atelet \
  --version "$SUBSTRATE_VERSION" --namespace "$ATE_NS" --wait --timeout 10m -f "$WORK/substrate-atelet-values.yaml"
kubectl rollout status daemonset/atelet -n "$ATE_NS" --timeout=5m
helm upgrade --install substrate oci://ghcr.io/kagent-dev/substrate/helm/substrate \
  --version "$SUBSTRATE_VERSION" --namespace "$ATE_NS" --wait --timeout 10m -f - <<'EOF'
atelet:
  enabled: false
preview:
  - PerTemplateSnapshotLocation
  - GoldenEgressPolicy
ateApi:
  authorization:
    bootstrapOwners:
      - spiffe://cluster.local/ns/kagent/sa/kagent-controller
      - system:serviceaccount:ate-system:ate-client
atenetRouter:
  ingressAllowedSPIFFEIDs:
    - spiffe://cluster.local/ns/kagent/sa/kagent-controller
credentialProvider:
  namespacePolicies:
    - atespace: kagent
      allowedNamespaces: [kagent]
EOF

echo "==> kagent"
# The chart's default ModelConfig reads this Secret. The Librarian uses its own ModelConfig for
# the in-cluster Ministral, so a placeholder is enough; replace it to use OpenAI models.
exists secret kagent-openai -n "$NS" || apply_secret kagent-openai "$NS" OPENAI_API_KEY "placeholder-not-a-real-key"
kubectl label namespace "$NS" ate.dev/api-access=true --overwrite >/dev/null
helm upgrade --install kagent oci://ghcr.io/kagent-dev/kagent/helm/kagent \
  --version "$KAGENT_VERSION" --namespace "$NS" --timeout 12m -f - <<'EOF'
controller:
  substrateClientCertificate:
    enabled: true
substrateWorkerPool:
  create: true
  workerImage: "ghcr.io/kagent-dev/substrate/ateworker-gvisor:v1.0.0"
EOF
kubectl rollout status deployment/kagent-controller -n "$NS" --timeout=300s
kubectl get pods -n "$ATE_NS"
kubectl get pods,workerpools -n "$NS"

cat <<EOF

kagent is installed. Next:
  make deploy                     # creates jar-mcp in JAR's namespace if not already deployed
  kubectl -n ${NS} apply -f config/manifests/k8s/kagent/example-librarian.yaml
  kubectl -n ${NS} get agents,remotemcpservers
  kubectl -n ${NS} port-forward svc/kagent-ui 8082:8080   # then open http://localhost:8082
EOF
