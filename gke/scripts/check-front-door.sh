#!/bin/sh
# Checks a rendered air-orchestrator-service release against the front-door
# requirements of the orchestrator's spec 0016 that can be read off a manifest.
# validate.sh runs it for uat and prod; those requirements are "deferred" to
# this repository in the spec's traceability table.
#
#   gke/scripts/check-front-door.sh <rendered-manifests.yaml>
#
#   REQ-FDR-002  the route forwards exactly the public paths
#   REQ-FDR-007  the health check is /v1/health
#   REQ-FDR-010  the response timeout is at least four times the turn deadline
#   REQ-FDR-011  an internal class, a TLS listener, and a NetworkPolicy that
#                admits address ranges and no namespace
#   REQ-FDR-012  a rate-limit policy is attached
#
# What a manifest can't show (a stream arriving unbuffered, a 404 for an
# internal path, a 429) is smoke-front-door.sh's, against the deployed address.
set -eu

file=${1:?usage: check-front-door.sh <rendered-manifests.yaml>}
# The service's default turn deadline in seconds (TURN__DEADLINE_MS=15000). If
# the values file raises it, raise TURN_DEADLINE_S here too: the two must move
# together.
turn_deadline_s=${TURN_DEADLINE_S:-15}
public_paths="/v1/chat /v1/query /v1/sessions"
fail=0
ok()  { printf '  ok    %-11s %s\n' "$1" "$2"; }
bad() { printf '  FAIL  %-11s %s\n' "$1" "$2"; fail=$((fail + 1)); }

# doc <Kind>: the one YAML document of that kind.
doc() {
  awk -v kind="$1" '
    /^---/ { if (hit) print buf; buf = ""; hit = 0; next }
    { buf = buf $0 "\n" }
    $0 == "kind: " kind { hit = 1 }
    END { if (hit) print buf }' "$file"
}

route=$(doc HTTPRoute)
gateway=$(doc Gateway)
health=$(doc HealthCheckPolicy)
backend=$(doc GCPBackendPolicy)
netpol=$(doc NetworkPolicy)

if [ -z "$route" ] || [ -z "$gateway" ] || [ -z "$health" ] || [ -z "$backend" ]; then
  bad REQ-FDR-002 "no Gateway, HTTPRoute, HealthCheckPolicy or GCPBackendPolicy rendered: is gateway.enabled true?"
  exit 1
fi

routed=$(printf '%s\n' "$route" | sed -n 's/^ *value: *//p' | sort | tr '\n' ' ' | sed 's/ $//')
wanted=$(printf '%s\n' $public_paths | sort | tr '\n' ' ' | sed 's/ $//')
if [ "$routed" = "$wanted" ]; then ok REQ-FDR-002 "the route forwards exactly: $routed"
else bad REQ-FDR-002 "the route forwards [$routed], the spec's public set is [$wanted]"; fi

path=$(printf '%s\n' "$health" | sed -n 's/^ *requestPath: *//p')
if [ "$path" = /v1/health ]; then ok REQ-FDR-007 "health check on /v1/health"
else bad REQ-FDR-007 "health check on '$path', must be /v1/health"; fi

timeout=$(printf '%s\n' "$backend" | sed -n 's/^    timeoutSec: *//p' | head -1)
need=$((turn_deadline_s * 4))
if [ "${timeout:-0}" -ge "$need" ]; then ok REQ-FDR-010 "response timeout ${timeout}s >= 4 x ${turn_deadline_s}s turn deadline"
else bad REQ-FDR-010 "response timeout ${timeout:-unset}s is below ${need}s (4 x the ${turn_deadline_s}s turn deadline)"; fi

class=$(printf '%s\n' "$gateway" | sed -n 's/^ *gatewayClassName: *//p')
case "$class" in
  gke-l7-rilb|gke-l7-cross-regional-internal-managed-mc|gke-l7-rilb-mc) ok REQ-FDR-011 "internal class ($class)" ;;
  *) bad REQ-FDR-011 "gatewayClassName '$class' is not an internal class: the front door must have a private address" ;;
esac
if printf '%s\n' "$gateway" | grep -q 'protocol: HTTPS'; then ok REQ-FDR-011 "TLS listener"
else bad REQ-FDR-011 "no HTTPS listener: gateway.tls.enabled must be true outside a rehearsal"; fi
if printf '%s\n' "$netpol" | grep -q 'ipBlock:'; then ok REQ-FDR-011 "NetworkPolicy admits the load balancer's ranges"
else bad REQ-FDR-011 "NetworkPolicy has no ipBlock: the load balancer can't reach the pods (networkPolicy.allowedCidrs)"; fi

if printf '%s\n' "$backend" | grep -q 'securityPolicy: .'; then ok REQ-FDR-012 "rate-limit policy attached ($(printf '%s\n' "$backend" | sed -n 's/^ *securityPolicy: *//p'))"
else bad REQ-FDR-012 "no securityPolicy: nothing limits the request rate"; fi

[ "$fail" -eq 0 ] || exit 1
