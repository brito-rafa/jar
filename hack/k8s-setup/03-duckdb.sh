MANIFEST="$(cd "$(dirname "$0")/../.." && pwd)/config/manifests/k8s/duckdb/example-duckdb.yaml"
TOKEN=$(openssl rand -hex 24); sed "s/CHANGE-ME-to-a-long-random-token/${TOKEN}/" "$MANIFEST" | kubectl -n jar apply -f - && kubectl -n jar rollout restart deploy/duckdb
echo "Quack token: ${TOKEN}"
