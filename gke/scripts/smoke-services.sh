#!/bin/sh
# Smoke test of the four services deployed with the shared chart
# (gke/helm/air-service), on kind or GKE.
#
#   KUBE_CONTEXT   required; e.g. kind-air-platform, or the GKE context
#   SERVICES       default: all four
#   KEYS_FILE      optional env file of raw API keys (on kind:
#                  gke/kind/.state/services.env). With it, one authenticated
#                  call per service is made as well. On GKE the raw keys exist
#                  only where you issued them; without the file the test stops
#                  at readiness.
#   EXPECT_CITATIONS  "true" when air-rag-service has documents loaded (kind)
#
# For each service:
#   1. the Deployment is rolled out,
#   2. `helm test` passes (the Service answers /v1/health and /v1/ready from
#      inside the cluster, through the NetworkPolicy),
#   3. /v1/ready says what it should for that service,
#   4. with KEYS_FILE: one real call.
# Calls from this machine go through `kubectl port-forward`. Prints no keys.
# Exits non-zero on any failure.
set -eu

: "${KUBE_CONTEXT:?set KUBE_CONTEXT}"
services=${SERVICES:-"air-classifier-service air-rag-service air-tools-gateway air-orchestrator-service"}
k() { kubectl --context "$KUBE_CONTEXT" "$@"; }
fail=0
ok()  { printf '  ok    %s\n' "$1"; }
bad() { printf '  FAIL  %s\n' "$1"; fail=$((fail + 1)); }

if [ -n "${KEYS_FILE:-}" ]; then
  # shellcheck disable=SC1090
  . "$KEYS_FILE"
fi

tmp=$(mktemp -d)
pf_pid=""
stop_forward() {
  [ -n "$pf_pid" ] || return 0
  kill "$pf_pid" 2>/dev/null || true
  wait "$pf_pid" 2>/dev/null || true
  pf_pid=""
}
cleanup() { stop_forward; rm -rf "$tmp"; }
trap cleanup EXIT

# forward <service>  -> sets $base to http://127.0.0.1:<port>
forward() {
  stop_forward
  : > "$tmp/pf.log"
  k -n "$1" port-forward "service/$1" :80 > "$tmp/pf.log" 2>&1 &
  pf_pid=$!
  i=0
  while [ $i -lt 50 ]; do
    port=$(sed -n 's/^Forwarding from 127\.0\.0\.1:\([0-9]*\).*/\1/p' "$tmp/pf.log" | head -1)
    [ -z "$port" ] || { base="http://127.0.0.1:$port"; return 0; }
    sleep 0.2; i=$((i + 1))
  done
  echo "port-forward to $1 did not start: $(cat "$tmp/pf.log")" >&2
  return 1
}
# json <python expression over `d`>  (reads the body from stdin)
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)" 2>/dev/null || echo "?"; }
post() { # path key body [header]
  curl -s -m 60 -X POST "$base$1" -H "X-API-Key: $2" -H 'Content-Type: application/json' -d "$3"
}

for service in $services; do
  echo "$service"
  if k -n "$service" rollout status "deployment/$service" --timeout=300s >/dev/null 2>&1; then
    ok "rolled out ($(k -n "$service" get "deployment/$service" -o jsonpath='{.status.readyReplicas}') ready)"
  else
    bad "rollout did not complete"; continue
  fi
  if helm --kube-context "$KUBE_CONTEXT" -n "$service" test "$service" >/dev/null 2>&1; then
    ok "helm test (health and ready, in-cluster)"
  else
    bad "helm test"
  fi
  forward "$service" || { bad "port-forward"; continue; }
  ready=$(curl -s -m 15 "$base/v1/ready")

  case "$service" in
    air-classifier-service)
      status=$(printf '%s' "$ready" | json 'd["status"]')
      down=$(printf '%s' "$ready" | json '",".join(c["name"] for c in d["components"] if not c["ready"])')
      case "$status" in
        ok) ok "ready: every tier and the cache" ;;
        degraded) bad "ready but degraded: $down not ready (gateway token, model servers or cloud keys)" ;;
        *) bad "not ready: $ready" ;;
      esac
      if [ -n "${CLASSIFIER_KEY:-}" ]; then
        body=$(post /v1/classify "$CLASSIFIER_KEY" '{"text":"absolutely terrible"}')
        [ "$(printf '%s' "$body" | json 'd["sentiment"]["label"]')" = negative ] \
          && ok "classify: negative, decided by $(printf '%s' "$body" | json 'd["decided_by"]')" \
          || bad "classify: $(printf '%s' "$body" | head -c 200)"
      fi ;;
    air-rag-service)
      [ "$(printf '%s' "$ready" | json 'd["ready"]')" = True ] \
        && ok "ready: air-model-gateway serves the embedding model" || bad "not ready: $ready"
      count=$(printf '%s' "$ready" | json 'd["document_count"]')
      if [ "$count" = 0 ]; then
        [ "${EXPECT_CITATIONS:-false}" = true ] && bad "index is empty" \
          || echo "  note  index is empty: mount documents and set INGESTION__SEED_DIR"
      else ok "index holds $count chunks"; fi
      if [ -n "${RAG_KEY:-}" ]; then
        n=$(post /v1/retrieve "$RAG_KEY" '{"query":"what is the return policy"}' | json 'len(d["citations"])')
        if [ "${EXPECT_CITATIONS:-false}" = true ]; then
          [ "$n" != "?" ] && [ "$n" -gt 0 ] && ok "retrieve: $n citations" || bad "retrieve: no citations"
        else
          [ "$n" != "?" ] && ok "retrieve: answered ($n citations)" || bad "retrieve failed"
        fi
      fi ;;
    air-tools-gateway)
      [ "$(printf '%s' "$ready" | json 'all(c["ok"] for c in d["checks"])')" = True ] \
        && ok "ready: $(printf '%s' "$ready" | json '", ".join(c["name"] for c in d["checks"])')" \
        || bad "not ready: $ready"
      if [ -n "${TOOLS_READ_KEY:-}" ]; then
        n=$(curl -s -m 15 "$base/v1/capabilities" -H "X-API-Key: $TOOLS_READ_KEY" | json 'len(d["capabilities"])')
        [ "$n" != "?" ] && [ "$n" -gt 0 ] && ok "capabilities: $n" || bad "capabilities"
        code=$(curl -s -m 15 -o /dev/null -w '%{http_code}' -X POST "$base/v1/actions/orders.cancel/execute" \
          -H "X-API-Key: $TOOLS_READ_KEY" -H 'Content-Type: application/json' -d '{}')
        [ "$code" = 403 ] && ok "read key cannot execute (403)" || bad "read key on execute answered $code"
      fi ;;
    air-orchestrator-service)
      [ "$(printf '%s' "$ready" | json 'd["ready"]')" = True ] \
        && ok "ready: air-model-gateway serves this service" || bad "not ready: $ready"
      for dep in air-classifier air-rag air-tools-gateway; do
        [ "$(printf '%s' "$ready" | json "[x['configured'] for x in d['dependencies'] if x['service']=='$dep'][0]")" = True ] \
          && ok "$dep configured" || bad "$dep is not configured"
      done
      if [ -n "${ORCHESTRATOR_CUSTOMER_KEY:-}" ]; then
        body=$(post /v1/chat "$ORCHESTRATOR_CUSTOMER_KEY" '{"message":"What is your return policy?"}')
        status=$(printf '%s' "$body" | json 'd["status"]')
        stages=$(printf '%s' "$body" | json '" ".join(s["stage"]+"="+s["status"] for s in d["trace"])')
        [ "$status" = ok ] && ok "chat turn: ok ($stages)" || bad "chat turn: $status $(printf '%s' "$body" | head -c 300)"
        degraded=$(printf '%s' "$body" | json '",".join(d["degraded"])')
        [ -z "$degraded" ] && ok "no dependency degraded" || bad "degraded: $degraded"
        if [ "${EXPECT_CITATIONS:-false}" = true ]; then
          n=$(printf '%s' "$body" | json 'len(d["citations"])')
          [ "$n" != "?" ] && [ "$n" -gt 0 ] && ok "answer cites $n source(s)" || bad "answer has no citation"
        fi
        code=$(curl -s -m 15 -o /dev/null -w '%{http_code}' -X POST "$base/v1/query" \
          -H "X-API-Key: $ORCHESTRATOR_CUSTOMER_KEY" -H 'Content-Type: application/json' -d '{"query":"x"}')
        [ "$code" = 403 ] && ok "customer key refused on the business route (403)" || bad "customer key on /v1/query answered $code"
      fi ;;
  esac
done

if [ "$fail" -eq 0 ]; then echo "smoke: all checks passed"; else echo "smoke: $fail check(s) failed"; exit 1; fi
