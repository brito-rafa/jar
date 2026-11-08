# cert-manager
helm install cert-manager oci://quay.io/jetstack/charts/cert-manager \
  --version v1.21.3 --namespace cert-manager --create-namespace \
  --set crds.enabled=true --wait

# kserve crds first
helm upgrade --install kserve-crd oci://ghcr.io/kserve/charts/kserve-crd \
  --version v0.20.0 --namespace kserve --create-namespace --wait

kubectl wait --for=condition=Established --timeout=60s \
  crd/clusterstoragecontainers.serving.kserve.io \
  crd/inferenceservices.serving.kserve.io \
  crd/servingruntimes.serving.kserve.io

# kserve without istio
helm upgrade --install kserve-resources oci://ghcr.io/kserve/charts/kserve-resources \
  --version v0.20.0 --namespace kserve --reuse-values \
  --set kserve.controller.gateway.disableIstioVirtualHost=true \
  --set kserve.controller.gateway.disableIngressCreation=true --wait

kubectl -n kserve get pods

echo "Create your CRs to serve models now..."
