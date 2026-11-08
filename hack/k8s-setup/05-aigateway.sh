helm upgrade -i eg oci://docker.io/envoyproxy/gateway-helm --version v1.9.2 \
  --namespace envoy-gateway-system --create-namespace \
  -f https://raw.githubusercontent.com/theagentrouter/agent-router/main/manifests/envoy-gateway-values.yaml

helm upgrade -i aieg-crd oci://docker.io/envoyproxy/ai-gateway-crds-helm --version v1.2.1 \
  --namespace envoy-ai-gateway-system --create-namespace
helm upgrade -i aieg oci://docker.io/envoyproxy/ai-gateway-helm --version v1.2.1 \
  --namespace envoy-ai-gateway-system --create-namespace
kubectl wait --timeout=2m -n envoy-ai-gateway-system deployment/ai-gateway-controller --for=condition=Available

kubectl apply -f ai-gateway.yaml


ENVOY_SVC=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=ai-gateway,gateway.envoyproxy.io/owning-gateway-namespace=default \
  -o jsonpath='{.items[0].metadata.name}')
kubectl port-forward -n envoy-gateway-system svc/$ENVOY_SVC 8080:80