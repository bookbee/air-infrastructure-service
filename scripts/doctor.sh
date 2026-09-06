#!/usr/bin/env bash
#
# Verify the shared local stack is actually serving what sibling AIR services
# expect of it. Every check states what it looked at, so a failure names the fix
# rather than just the symptom.
#
# Run via `make doctor` — it supplies NETWORK, PORT and COMPOSE.
set -uo pipefail

NETWORK="${NETWORK:-air-net}"
PORT="${PORT:-8080}"
COMPOSE="${COMPOSE:-docker compose}"
# One of the tokens .env.example provisions, so a stock stack needs no edits.
DOCTOR_API_KEY="${DOCTOR_API_KEY:-air-platform-dev}"

fail=0
warn=0

ok()   { printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$1"; fail=$((fail + 1)); }
note() { printf '  \033[33m!\033[0m %s\n' "$1"; warn=$((warn + 1)); }
hdr()  { printf '\n\033[1m%s\033[0m\n' "$1"; }

hdr "Docker"
if docker info >/dev/null 2>&1; then
  ok "daemon reachable"
else
  bad "docker daemon is not reachable — start Docker Desktop"
  echo
  echo "Nothing else can be checked. Aborting."
  exit 1
fi

if docker network inspect "$NETWORK" >/dev/null 2>&1; then
  attached=$(docker network inspect "$NETWORK" \
    --format '{{range $k, $v := .Containers}}{{$v.Name}} {{end}}' 2>/dev/null)
  ok "network '$NETWORK' exists"
  [ -n "${attached// /}" ] && printf '      attached: %s\n' "$attached"
else
  bad "network '$NETWORK' is missing — run 'make up' here before starting sibling services"
fi

hdr "Stores"
if $COMPOSE exec -T redis redis-cli -a localdevpass ping 2>/dev/null | grep -q PONG; then
  ok "redis answers AUTH'd PING (password: localdevpass)"
else
  bad "redis is not answering an authenticated PING — check 'make ps'"
fi

if $COMPOSE exec -T postgres pg_isready -U air -d air >/dev/null 2>&1; then
  ok "postgres accepting connections"
else
  bad "postgres is not ready"
fi

if nc -z localhost 6432 >/dev/null 2>&1; then
  ok "pgbouncer listening on :6432"
else
  bad "pgbouncer is not listening on :6432"
fi

if $COMPOSE exec -T mongo mongosh --quiet --eval 'db.adminCommand("ping").ok' 2>/dev/null | grep -q 1; then
  ok "mongo answers ping"
else
  bad "mongo is not answering"
fi

hdr "air-infrastructure-service"
if curl -sf -m 3 "http://127.0.0.1:${PORT}/v1/health" >/dev/null 2>&1; then
  ok "healthy on :${PORT} (compose service 'air-infrastructure-service')"
else
  bad "not healthy on :${PORT} — 'make logs' for why"
fi

if curl -sf -m 3 "http://127.0.0.1:${PORT}/v1/ready" >/dev/null 2>&1; then
  ok "ready on :${PORT}"
else
  bad "not ready on :${PORT} — 'make logs' for why"
fi

# Liveness only says the process answers. These two check the thing the service
# actually exists to do, which is what a consumer's first failure will be about.
grant=$(curl -sf -m 3 -X POST "http://127.0.0.1:${PORT}/v1/resources/postgres/credentials" \
  -H "X-API-Key: ${DOCTOR_API_KEY}" -H 'Content-Type: application/json' -d '{}' 2>/dev/null)
if [ -n "$grant" ] && printf '%s' "$grant" | grep -q '"password"'; then
  ok "brokers a postgres credential for '${DOCTOR_API_KEY}'"
else
  bad "could not broker a credential — check AIR_INFRASTRUCTURE_SERVICE__SECURITY__SERVICE_TOKENS in .env"
  [ -n "$grant" ] && printf '      response: %s\n' "$grant"
fi

# A consumer resolves this service by compose service name, not by localhost.
# Checking it from inside the network is the only way to catch a break here.
if docker run --rm --network "$NETWORK" curlimages/curl:latest \
     -sf -m 5 "http://air-infrastructure-service:8080/v1/health" >/dev/null 2>&1; then
  ok "reachable as 'air-infrastructure-service:8080' from inside '$NETWORK'"
else
  note "could not verify in-network DNS (needs the curlimages/curl image; skipped offline)"
fi

hdr "Observability"
if curl -sf -m 3 http://127.0.0.1:9090/-/ready >/dev/null 2>&1; then
  ok "prometheus ready on :9090"
  down_targets=$(curl -s -m 3 'http://127.0.0.1:9090/api/v1/targets?state=active' 2>/dev/null \
    | tr ',' '\n' | grep -c '"health":"down"' || true)
  if [ "${down_targets:-0}" -gt 0 ]; then
    note "$down_targets scrape target(s) down — expected for AIR services that are not running"
  else
    ok "all scrape targets up"
  fi
else
  note "prometheus not running (optional) — 'make up-observability' to start it"
fi

hdr "Summary"
if [ "$fail" -eq 0 ] && [ "$warn" -eq 0 ]; then
  printf '  \033[32mall checks passed\033[0m\n\n'
elif [ "$fail" -eq 0 ]; then
  printf '  \033[33m%d warning(s), no failures\033[0m\n\n' "$warn"
else
  printf '  \033[31m%d failure(s)\033[0m, %d warning(s)\n\n' "$fail" "$warn"
fi
exit $((fail > 0 ? 1 : 0))
