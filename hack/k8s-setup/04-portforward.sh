TS=$(tailscale ip -4)
kubectl port-forward --address 127.0.0.1,$TS svc/ministral-3-8b-predictor    8080:80   &
kubectl port-forward --address 127.0.0.1,$TS svc/granite-embedding-predictor 8081:80   &
kubectl port-forward --address 127.0.0.1,$TS svc/duckdb                      9494:9494 &
kubectl port-forward deploy/duckdb                   4213:4213 &
# run on foreground and a control-c kills it
trap 'kill $(jobs -p)' INT; wait
