#!/bin/sh
# Every static check of the deployment code, for all five services
# (`make gke-validate`, and what CI runs):
#
#   1. both charts lint,
#   2. every service renders for uat, prod and kind, and every manifest passes
#      its Kubernetes schema (kubeconform),
#   3. Terraform is formatted and each environment validates,
#   4. the shell scripts pass shellcheck.
#
# A render that fails is a failure here: the output is checked before it is
# piped on, so an empty render can't pass as "0 resources, all valid".
#
# Needs helm, kubeconform and terraform; shellcheck when installed. The
# gateway's chart needs its config files: GATEWAY_DIR (default: the
# air-model-gateway checkout next to this repository).
set -eu

gke=$(cd "$(dirname "$0")/.." && pwd)
release="$gke/scripts/release.sh"
services="air-model-gateway air-classifier-service air-rag-service air-tools-gateway air-orchestrator-service"
kube_version=${KUBE_VERSION:-1.33.0}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "charts"
SAMPLE=1 "$release" render air-model-gateway prod > /dev/null   # fails early if GATEWAY_DIR is wrong
gateway_dir=${GATEWAY_DIR:-$gke/../../air-model-gateway}
helm lint "$gke/helm/air-model-gateway" -f "$gke/values/prod/air-model-gateway.yaml" \
  --set-file "config.policy=$gateway_dir/deploy/policy/policy.prod.yaml" \
  --set image.repository=sample/air-model-gateway --set secrets.gcp.project=sample --quiet
helm lint "$gke/helm/air-service" -f "$gke/values/prod/air-tools-gateway.yaml" \
  --set image.repository=sample/air-tools-gateway --set image.tag=sample --set secrets.gcp.project=sample --quiet
echo "  ok    helm lint: air-model-gateway, air-service"

echo "manifests"
for env in uat prod kind; do
  for service in $services; do
    out="$tmp/$service-$env.yaml"
    SAMPLE=1 KUBE_VERSION=$kube_version "$release" render "$service" "$env" > "$out"
    grep -q '^kind: Deployment' "$out" || { echo "  FAIL  $service $env rendered no Deployment"; exit 1; }
    summary=$(kubeconform -strict -summary -kubernetes-version "$kube_version" \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json' \
      "$out" | tail -1) || { echo "  FAIL  $service $env: $summary"; exit 1; }
    printf '  ok    %-26s %-5s %s\n' "$service" "$env" "$(echo "$summary" | sed 's/^Summary: //; s/ in 1 file//')"
  done
done

echo "terraform"
terraform -chdir="$gke/terraform" fmt -recursive -check
for env in uat prod; do
  terraform -chdir="$gke/terraform/environments/$env" init -backend=false -input=false > /dev/null
  terraform -chdir="$gke/terraform/environments/$env" validate -no-color > /dev/null
  echo "  ok    $env"
done

echo "scripts"
if command -v shellcheck > /dev/null 2>&1; then
  shellcheck -S warning "$gke"/scripts/*.sh "$gke"/kind/*.sh
  echo "  ok    shellcheck"
else
  echo "  skip  shellcheck is not installed"
fi
echo "gke-validate: all checks passed"
