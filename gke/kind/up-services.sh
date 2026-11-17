#!/bin/sh
# Deploys the four services on the shared chart to the kind cluster that
# up-gateway.sh created (`make kind-up` runs both): air-classifier-service,
# air-rag-service, air-tools-gateway and air-orchestrator-service.
#
#   1. each image, built from the sibling checkout and loaded into the cluster
#   2. each service's secrets as a Kubernetes Secret: a generated salt and API
#      keys (digests computed here), its air-model-gateway token from
#      up-gateway.sh, and the rediss:// URL of the TLS Redis stand-in
#   3. air-rag-service's sample corpus as a ConfigMap
#   4. `helm upgrade --install` of gke/helm/air-service with the PRODUCTION
#      values and the kind overlay on top, so what runs is production's shape
#
# Generated state lives in .state/ (gitignored); re-running keeps it. Every
# kubectl/helm call names the kind context explicitly. Prints no secrets.
#
# AIR_ROOT is the folder holding the sibling checkouts (default: the parent of
# this repository). SERVICES limits the run to some of them.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
gke=$(cd "$here/.." && pwd)
root=$(cd "${AIR_ROOT:-$gke/../..}" && pwd)
cluster=air-platform
ctx="kind-$cluster"
deps=air-model-gateway-deps
state="$here/.state"
services=${SERVICES:-"air-classifier-service air-rag-service air-tools-gateway air-orchestrator-service"}
gateway_url=http://air-model-gateway.air-model-gateway.svc.cluster.local
k() { kubectl --context "$ctx" "$@"; }
apply_secret() { # namespace name args...  (create or update)
  n=$1; name=$2; shift 2
  k -n "$n" create secret generic "$name" "$@" --dry-run=client -o yaml | k apply -f - >/dev/null
}
digest() { "$gke/scripts/hash-key.sh" "$1" "$2"; }

[ -f "$state/tokens.env" ] && [ -f "$state/redis-password" ] || {
  echo "run up-gateway.sh first (make kind-up does): $state has no gateway tokens" >&2; exit 1; }

# Raw keys for this kind stack only, generated once. Not the published
# development keys: the tools gateway runs as `production` here, as it does on
# GKE, and refuses the development salt.
if [ ! -f "$state/services.env" ]; then
  {
    for name in CLASSIFIER_SALT RAG_SALT TOOLS_SALT ORCHESTRATOR_SALT; do
      echo "$name=$(openssl rand -hex 24)"
    done
    echo "CLASSIFIER_KEY=airc_kind_$(openssl rand -hex 16)"
    echo "RAG_KEY=airr_kind_$(openssl rand -hex 16)"
    echo "TOOLS_READ_KEY=airtg_read_kind_$(openssl rand -hex 16)"
    echo "TOOLS_EXECUTE_KEY=airtg_exec_kind_$(openssl rand -hex 16)"
    echo "ORCHESTRATOR_CUSTOMER_KEY=airo_kind_customer_$(openssl rand -hex 16)"
    echo "ORCHESTRATOR_BUSINESS_KEY=airo_kind_business_$(openssl rand -hex 16)"
  } > "$state/services.env"
fi
chmod 600 "$state"/*
# shellcheck disable=SC1091
. "$state/services.env"
# shellcheck disable=SC1091
. "$state/tokens.env"
redis_password=$(cat "$state/redis-password")
redis_host="redis.$deps.svc.cluster.local:6378"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

for service in $services; do
  dir="$root/$service"
  [ -f "$dir/Dockerfile" ] || { echo "missing $dir: check out $service next to air-infrastructure-service" >&2; exit 1; }
  image="$service:local"
  echo "$service: building and loading $image"
  docker build -q -t "$image" "$dir" >/dev/null
  kind load docker-image "$image" --name "$cluster" >/dev/null

  k create namespace "$service" --dry-run=client -o yaml | k apply -f - >/dev/null
  files="$tmp/$service"
  mkdir -p "$files"
  ca=no
  case "$service" in
    air-classifier-service)
      printf '%s' "$CLASSIFIER_SALT" > "$files/AIR_CLASSIFIER__SECURITY__HASH_SALT"
      printf '[{"id":"kind","name":"kind","key_hash":"%s","scopes":["classify:read","admin:read"]}]' \
        "$(digest "$CLASSIFIER_SALT" "$CLASSIFIER_KEY")" > "$files/AIR_CLASSIFIER__SECURITY__API_KEYS_INLINE"
      printf '%s' "$AIR_CLASSIFIER_SERVICE_TOKEN" > "$files/AIR_CLASSIFIER__MODEL_GATEWAY__TOKEN"
      printf 'rediss://:%s@%s/0?ssl_cert_reqs=required&ssl_ca_certs=/etc/air/redis-ca/ca.pem' \
        "$redis_password" "$redis_host" > "$files/AIR_CLASSIFIER__CACHE__REDIS_URL"
      ca=yes ;;
    air-rag-service)
      printf '%s' "$RAG_SALT" > "$files/AIR_RAG_SERVICE__SECURITY__HASH_SALT"
      printf '[{"id":"kind","name":"kind","key_hash":"%s","tenant":"brightcart","scopes":["retrieval:read","admin:read"]}]' \
        "$(digest "$RAG_SALT" "$RAG_KEY")" > "$files/AIR_RAG_SERVICE__SECURITY__API_KEYS_INLINE"
      printf '%s' "$AIR_RAG_SERVICE_TOKEN" > "$files/AIR_RAG_SERVICE__MODEL_GATEWAY__API_KEY"
      k -n "$service" create configmap air-rag-documents --from-file="$dir/sit/corpus" \
        --dry-run=client -o yaml | k apply -f - >/dev/null ;;
    air-tools-gateway)
      printf '%s' "$TOOLS_SALT" > "$files/AIR_TOOLS_GATEWAY__SECURITY__HASH_SALT"
      printf '[{"id":"brightcart-read","tenant":"brightcart","scope":"read","key_hash":"%s"},{"id":"brightcart-exec","tenant":"brightcart","scope":"execute","key_hash":"%s"}]' \
        "$(digest "$TOOLS_SALT" "$TOOLS_READ_KEY")" "$(digest "$TOOLS_SALT" "$TOOLS_EXECUTE_KEY")" \
        > "$files/AIR_TOOLS_GATEWAY__SECURITY__API_KEYS"
      printf 'rediss://:%s@%s/0' "$redis_password" "$redis_host" > "$files/AIR_TOOLS_GATEWAY__STORE__REDIS_URL"
      ca=yes ;;
    air-orchestrator-service)
      printf '%s' "$ORCHESTRATOR_SALT" > "$files/AIR_ORCHESTRATOR_SERVICE__SECURITY__HASH_SALT"
      printf '[{"id":"kind-customer","name":"kind customer","key_hash":"%s","channel":"customer","tenant":"brightcart","scopes":["chat:write","session:read","admin:read"],"allow_actions":true},{"id":"kind-business","name":"kind business","key_hash":"%s","channel":"business","tenant":"brightcart","scopes":["query:write","session:read","admin:read"],"allow_actions":false}]' \
        "$(digest "$ORCHESTRATOR_SALT" "$ORCHESTRATOR_CUSTOMER_KEY")" "$(digest "$ORCHESTRATOR_SALT" "$ORCHESTRATOR_BUSINESS_KEY")" \
        > "$files/AIR_ORCHESTRATOR_SERVICE__SECURITY__API_KEYS_INLINE"
      printf '%s' "$AIR_ORCHESTRATOR_SERVICE_TOKEN" > "$files/AIR_ORCHESTRATOR_SERVICE__MODEL_GATEWAY__API_KEY"
      printf '%s' "$CLASSIFIER_KEY" > "$files/AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__CLASSIFIER__API_KEY"
      printf '%s' "$RAG_KEY" > "$files/AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__RAG__API_KEY"
      printf '{"brightcart":{"read":"%s","execute":"%s"}}' "$TOOLS_READ_KEY" "$TOOLS_EXECUTE_KEY" \
        > "$files/AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__TOOLS_GATEWAY__TENANT_KEYS" ;;
    *) echo "unknown service $service" >&2; exit 2 ;;
  esac
  apply_secret "$service" "$service-secrets" --from-file="$files"
  [ "$ca" = no ] || apply_secret "$service" redis-ca --from-file=ca.pem="$state/ca.crt"

  existed=$(helm --kube-context "$ctx" -n "$service" status "$service" >/dev/null 2>&1 && echo yes || echo no)
  echo "$service: helm upgrade --install (values/prod + values/kind)"
  helm --kube-context "$ctx" upgrade --install "$service" "$gke/helm/air-service" -n "$service" \
    -f "$gke/values/prod/$service.yaml" -f "$gke/values/kind/$service.yaml" \
    --wait --timeout 8m >/dev/null
  if [ "$existed" = yes ]; then
    # Secrets and the :local image are read at start; Helm can't see them change.
    k -n "$service" rollout restart "deployment/$service" >/dev/null
    k -n "$service" rollout status "deployment/$service" --timeout=480s >/dev/null
  fi
done

echo
k get pods -A -l app.kubernetes.io/part-of=air-platform -o wide
echo
echo "kind: services deployed (gateway at $gateway_url). Smoke test: make kind-test"
echo "      generated keys: gke/kind/.state/services.env"
