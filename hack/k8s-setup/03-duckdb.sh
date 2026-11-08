TOKEN=$(openssl rand -hex 24); sed "s/CHANGE-ME-to-a-long-random-token/${TOKEN}/" duckdb.yaml | kubectl apply -f - && kubectl rollout restart deploy/duckdb
echo "Quack token: ${TOKEN}"
