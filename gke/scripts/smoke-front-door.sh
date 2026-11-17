#!/bin/sh
# Smoke test of air-orchestrator-service's front door, from where a caller
# stands: a machine that can reach the private address (a VM in the VPC, a VPN,
# or the shop backend's own network). It proves the requirements of the
# orchestrator's spec 0016 that only a deployed load balancer can show.
#
#   FRONT_DOOR_URL   required, e.g. https://air.uat.internal.example
#   ORCHESTRATOR_CUSTOMER_KEY   required: a raw customer-channel API key
#   CA_FILE          optional: the CA of the front door's certificate, when it
#                    is not one this machine already trusts
#   RATE_BURST       optional: send this many requests at once and expect at
#                    least one 429 (AC-6). Set it above the configured limit
#                    (terraform: front_door_rate_limit_count). Unset: skipped,
#                    because the burst spends that minute's allowance.
#   EXPECT_INTERNAL_404  default true. false when pointed straight at the
#                    service (a port-forward) to try the script itself: the
#                    service answers its internal routes, a front door doesn't.
#
#   AC-4 / REQ-FDR-010  a streamed answer arrives as it is written
#   AC-5 / REQ-FDR-002  internal paths are a 404 from the front door
#   AC-6 / REQ-FDR-012  the rate limit answers 429
#   REQ-FDR-004         X-Request-ID comes back unchanged
#   REQ-FDR-011         TLS, when the URL is https
#
# Prints no key. Exits non-zero on any failure.
set -eu

: "${FRONT_DOOR_URL:?set FRONT_DOOR_URL, e.g. https://air.uat.internal.example}"
: "${ORCHESTRATOR_CUSTOMER_KEY:?set ORCHESTRATOR_CUSTOMER_KEY}"
base=${FRONT_DOOR_URL%/}
fail=0
ok()   { printf '  ok    %s\n' "$1"; }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail + 1)); }
skip() { printf '  skip  %s\n' "$1"; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# The key goes to curl through a config file, so it never appears in `ps`.
umask 077
{
  printf 'header = "X-API-Key: %s"\n' "$ORCHESTRATOR_CUSTOMER_KEY"
  printf 'header = "Content-Type: application/json"\n'
  [ -z "${CA_FILE:-}" ] || printf 'cacert = "%s"\n' "$CA_FILE"
} > "$tmp/curl.cfg"
c() { curl -s -K "$tmp/curl.cfg" "$@"; }

echo "front door $base"

# ---- one ordinary turn, and the request id -----------------------------------
rid="smoke-$(date +%s)-$$"
code=$(c -m 60 -o "$tmp/chat.json" -D "$tmp/chat.headers" -w '%{http_code}' \
  -X POST "$base/v1/chat" -H "X-Request-ID: $rid" -d '{"message":"What is your return policy?"}') || code=000
if [ "$code" = 200 ]; then ok "chat turn answers 200"
else bad "chat turn answered $code: $(head -c 200 "$tmp/chat.json" 2>/dev/null)"; fi
if grep -qi "^x-request-id: $rid" "$tmp/chat.headers" 2>/dev/null; then ok "X-Request-ID passed through unchanged (REQ-FDR-004)"
else bad "X-Request-ID was not echoed as sent (REQ-FDR-004)"; fi
case "$base" in
  https://*) ok "TLS handshake completed (REQ-FDR-011)" ;;
  *) skip "not https: TLS not checked (REQ-FDR-011)" ;;
esac

# ---- internal paths stay inside (AC-5) ---------------------------------------
if [ "${EXPECT_INTERNAL_404:-true}" = true ]; then
  for path in /metrics /v1/ready /v1/capabilities /v1/health /docs /openapi.json; do
    code=$(c -m 15 -o /dev/null -w '%{http_code}' "$base$path") || code=000
    if [ "$code" = 404 ]; then ok "$path is not forwarded (404)"
    else bad "$path answered $code through the front door: only the public paths may be forwarded (REQ-FDR-002)"; fi
  done
else
  skip "internal paths (EXPECT_INTERNAL_404=false)"
fi

# ---- a stream arrives as it is written (AC-4) --------------------------------
# Each received line is stamped on arrival. A buffering hop delivers everything
# in one burst at the end: first and last event then arrive together.
c -N -m 90 -X POST "$base/v1/chat" -H 'Accept: text/event-stream' \
  -d '{"message":"Explain your returns and refunds policy in detail."}' 2>/dev/null \
  | python3 -c '
import sys, time
first = last = None
kinds = []
for line in sys.stdin:
    if line.startswith("event:"):
        now = time.monotonic()
        first = first if first is not None else now
        last = now
        kinds.append(line.split(":", 1)[1].strip())
if first is None:
    print("none 0 0")
else:
    print(",".join(sorted(set(kinds))), len(kinds), int((last - first) * 1000))
' > "$tmp/stream.txt" || true
read -r kinds count spread < "$tmp/stream.txt" || true
case ",${kinds:-none}," in
  *,turn.start,*) case ",$kinds," in *,turn.end,*) ok "stream carried $count events, turn.start to turn.end" ;; *) bad "stream ended without turn.end (cut off? check gateway.timeoutSec)" ;; esac ;;
  *) bad "no event stream came back (${kinds:-nothing})" ;;
esac
if [ "${spread:-0}" -ge 200 ]; then ok "events arrived over ${spread} ms: not buffered (REQ-FDR-010)"
else bad "all events arrived within ${spread:-0} ms: something between here and the service is buffering the stream (REQ-FDR-010)"; fi

# ---- the rate limit (AC-6) ---------------------------------------------------
if [ -n "${RATE_BURST:-}" ]; then
  i=0
  while [ "$i" -lt "$RATE_BURST" ]; do
    c -m 20 -o /dev/null -w '%{http_code}\n' "$base/v1/sessions/sess_smoke_$i" >> "$tmp/burst.txt" &
    i=$((i + 1))
    # Bounded fan-out: 20 at a time.
    [ $((i % 20)) -ne 0 ] || wait
  done
  wait
  limited=$(grep -c '^429$' "$tmp/burst.txt" || true)
  if [ "$limited" -gt 0 ]; then ok "$limited of $RATE_BURST requests answered 429 (REQ-FDR-012)"
  else bad "none of $RATE_BURST requests was limited: is gateway.securityPolicy set, and RATE_BURST above the limit? (REQ-FDR-012)"; fi
else
  skip "rate limit (set RATE_BURST above the configured limit to check it)"
fi

if [ "$fail" -eq 0 ]; then echo "front door: all checks passed"; else echo "front door: $fail check(s) failed"; exit 1; fi
