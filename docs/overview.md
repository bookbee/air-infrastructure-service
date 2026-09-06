# air-infrastructure-service — Project Overview

**Status:** Active, local-dev tooling · **Last updated:** 2026-09-06
**Companion doc:** [`README.md`](../README.md) (day-to-day quick start and commands)

> **Convention note:** every `air-*` repo carries a file at this same path —
> `docs/overview.md` — with these same section headings, so `air-client`
> can walk the sibling repos and assemble one cross-project presentation from
> them. Keep the H2 headings below identical if you copy this template into
> another repo; only the content should change.

---

## Summary

`air-infrastructure-service` is the shared **local-development control plane**
for the AIR platform: a docker-compose stack that hosts Redis, Postgres, and
MongoDB, plus a small Go service that hands out **scoped, policy-checked
credentials** to those stores instead of every sibling service hardcoding
endpoints and passwords. It is the thing every other `air-*` repo depends on to
run locally without standing up its own copy of the data layer.

The runtime is a single static binary in a distroless image: no interpreter, no
virtualenv, no package manager to provision or patch.

**This project is explicitly not a production service.** In every deployed
environment, DevOps provisions Redis/Postgres/Mongo/observability directly,
and consumers connect to those managed instances the same way they'd connect
to anything else. This repo exists purely to make the laptop experience match
that shape closely enough that the credential-broker *pattern* consumers code
against doesn't change between local and prod — but the process itself never
runs in prod.

Model inference lives in [`air-llm`](../../air-llm), which *is* meant to run in
every environment, including production. See that repo's own `docs/overview.md`.

---

## Needs

### Problem

Every AIR service (`air-orchestrator-service`, `air-classifier-service`,
`air-client`, …) needs the same local dependencies: a Redis, a Postgres, a
MongoDB. Left alone, each service would wire those up itself — its own
containers, its own port choices, its own idea of where credentials live. That
produces N slightly different local environments and no single place to reason
about "does the platform come up on a clean laptop."

The team taking this forward is DevOps, so the artifact matters as much as the
behaviour: a process whose entire job is to answer one question over HTTP should
not arrive with an interpreter version and a dependency tree to keep patched.

### Goals

| # | Goal | Signal it's met |
|---|---|---|
| G1 | One-command local platform | `make up` yields healthy Redis, Postgres, Mongo, and this service |
| G2 | Brokered store access, not hardcoded endpoints | Consumers call `POST /v1/resources/{name}/credentials`; nothing in a sibling repo hardcodes a password |
| G3 | Per-service access control | RBAC (service × resource × action) + rate limits, config-driven |
| G4 | Predictable shared ports | One port map (`README.md`) every repo's compose file agrees with |
| G5 | Cheap to reason about | Small enough surface that a new engineer reads it in under an hour |
| G6 | Cheap to operate | A static binary in a distroless image: nothing to provision, no runtime to patch |

### Non-goals

- **Not a wire-protocol proxy.** This service is never on the data path for
  Redis/Postgres/Mongo queries — it hands out an endpoint + credentials and
  steps aside (pgbouncer is the one exception: a proven pooler, not something
  built here). See [Design](#design) for why.
- **Not production infrastructure.** No prod deployment target, no SLA, no
  on-call rotation. DevOps owns the real Redis/Postgres/Mongo/observability
  that `air-*` services talk to outside a laptop.
- **Not the model gateway.** That's `air-llm`, a separate, independently
  deployed service.

---

## Architecture

```text
   air-orchestrator-service   air-classifier-service   air-client
             │                        │                     │
             └────────── HTTP + X-API-Key ──────────────────┘
                          │
                          ▼
        ┌────────────────────────────────────────┐
        │       air-infrastructure-service        │
        │   Registry · Broker · Policy · Secrets  │
        └───┬───────────────┬──────────────────┬──┘
   endpoint  │  + scoped     │ admin/health     │
   & creds   │  creds        │ probes           │
             ▼               ▼                  ▼
     apps connect DIRECTLY   Redis · Postgres (pgbouncer) · Mongo
     to the store using
     brokered creds
```

One request shape reaches this service: *"give me a connection to `X`."* It
checks policy, returns an **endpoint + scoped credentials**, and gets out of
the way — the app opens the actual connection itself. It never sees a business
query.

### Components

| Component | File(s) | Responsibility | On the data path? |
|---|---|---|---|
| **Registry** | [`internal/registry/`](../internal/registry/registry.go) | Resource name → kind → per-environment endpoint | No |
| **Broker** | [`internal/broker/broker.go`](../internal/broker/broker.go) | Turns "I want resource X" into a scoped credential via a pluggable secret backend | No |
| **Secret backend** | [`internal/broker/secrets.go`](../internal/broker/secrets.go) | `env` (local, implemented) / `file` (implemented, unused by default) / `vault` (stub) | No |
| **Policy engine** | [`internal/policy/engine.go`](../internal/policy/engine.go) | RBAC: `(service, resource, action)` → allow/deny, loaded from YAML | Decision only |
| **Rate limiter** | [`internal/policy/ratelimit.go`](../internal/policy/ratelimit.go) | Per-`(service, resource)` token bucket | Decision only |
| **Security** | [`internal/security/apikeys.go`](../internal/security/apikeys.go) | `X-API-Key` → `Principal` | Yes (authN) |
| **Stores health** | [`internal/stores/health.go`](../internal/stores/health.go) | Lightweight TCP probes for `/v1/ready` reporting | No |
| **Observability** | [`internal/observability/`](../internal/observability/) | Structured logs (`log/slog`), Prometheus metrics | — |
| **Client SDK** | [`client/`](../client/client.go) | Consumer-side: fetch a grant, return a connection string | — |

### Request flow (the only one that matters)

```text
app: client.PostgresDSN(ctx)
   └─▶ POST /v1/resources/postgres/credentials   (X-API-Key = service token)
          authN → policy(service, postgres, connect) → broker
          ◀── { host: pgbouncer, port: 6432, db, user, password }
   └─▶ app opens its own pooled connection directly to pgbouncer
```

---

## Design

| Decision | Choice | Why |
|---|---|---|
| Store access shape | **Broker + client SDK**, not a proxy | Each store speaks a different wire protocol; a faithful proxy either reimplements a lot or wraps an existing one. Store-native ACLs + a broker deciding *who may connect* give the same control without a fragile, stateful hop on every query. |
| Postgres pooling | **pgbouncer**, the one on-path exception | Connection multiplexing is the one place a proxy clearly pays off — use a proven one, don't build it. |
| Language / runtime | **Go 1.25, stdlib `net/http`** | One static binary, distroless image, nothing to provision. `http.ServeMux`'s method-and-wildcard patterns cover the whole surface, so no web framework is needed. |
| Dependencies | Two direct (`prometheus/client_golang`, `gopkg.in/yaml.v3`) | Every dependency is something DevOps inherits and patches. Env decoding, routing, JSON, logging and TCP probes are all stdlib. |
| Config | `AIR_INFRASTRUCTURE_SERVICE__*` env vars, `__` for nesting, `environment` selector | One binary, environment chosen at boot. Matches how every other AIR service names its settings. |
| Secrets | Pluggable `SecretBackend` interface | `env` today; `file`/`vault` are shaped as drop-ins so a backend swap is config, not a rewrite. |
| Readiness semantics | `/v1/ready` reports store health, **never gates on it** | The broker hands out an endpoint and steps aside — a red store fails the *consumer's* connection, not this process. Gating would pull the broker out of rotation (and stop issuing credentials) at exactly the moment a consumer needs fresh ones. |
| Policy default | `default_allow: true` in `policy.local.yaml` | A **local-only convenience**. A stricter environment uses a document with `default_allow: false` and explicit grants. |
| Error rendering | RFC 9457 problem+json, handlers return `error` | One writer for every failure, so no handler can invent a response shape. Sentinel errors are wrapped, not concatenated, so a sentinel's text never reaches a caller's `detail`. |
| Client SDK shape | Returns a DSN, not a live driver client | A Go consumer already pins its own driver. Returning a connection string keeps `client/` dependency-free — importing it constrains nothing. |

---

## Implementation

### Stack

Go 1.25 · stdlib `net/http` (`ServeMux` pattern routing) · `log/slog` ·
`prometheus/client_golang` · `gopkg.in/yaml.v3` · stdlib `testing` + `httptest` ·
`gofmt`, `go vet`, `staticcheck` (golangci-lint config included for CI).

### Repo layout

```text
air-infrastructure-service/
├── go.mod, Dockerfile, docker-compose.yml, Makefile, .env.example, .golangci.yml
├── cmd/air-infrastructure-service/     # flags, signals, graceful shutdown
├── internal/
│   ├── app/            # wiring + routing
│   ├── config/         # AIR_INFRASTRUCTURE_SERVICE__* decoding, .env, precedence
│   ├── constants/      # Environment / ResourceKind / Action / Version
│   ├── api/            # problem+json, middleware, auth and policy gates
│   │   └── v1/         # system, resources, admin handlers
│   ├── schema/         # wire types (JSON tags are the wire contract)
│   ├── registry/  broker/  policy/  security/  stores/  observability/
├── client/                             # the Go SDK consumers import
├── deploy/                             # redis, postgres, mongo, prometheus, policy
├── scripts/doctor.sh
└── docs/{01-setup.md,overview.md}
```

`internal/` for everything the platform does not import; `client/` is the only
exported package, so the public API stays deliberate.

### Testing & tooling

Tests cover config precedence and parsing, policy decisions and rate limiting,
broker issuance and its two distinguishable failures, the API key verifier, the
Go client, and the full HTTP surface (auth, resource scoping, credential
issuance, unknown resource → 404, unmatched path, request ids, metric labels,
readiness reporting). All hermetic: nothing needs to be running.

`make check` runs lint + vet + tests and is the gate before calling work done.
`make doctor` is the runtime equivalent: it checks the stores, this service's
liveness and readiness, that it brokers a working credential, and that siblings
can resolve it by service name on `air-net`.

---

## Demo Guide

**Audience:** other AIR engineers, ~10–15 minutes, live terminal + browser.
The goal is to show *why this exists* (brokered creds, not hardcoded endpoints)
and prove it's not just a shape on paper.

### Setup (before the room fills up)

```bash
make env && make up      # redis, postgres, pgbouncer, mongo, this service
make doctor              # confirm everything's green before you're on stage
```

### Script

1. **The pitch (1 min).** "Every AIR service needs Redis/Postgres/Mongo. This
   repo hosts them once for the whole platform and hands out *scoped
   credentials* instead of every service hardcoding a password." Show the
   [Architecture](#architecture) diagram.

2. **Liveness vs. readiness (1 min).**
   ```bash
   curl localhost:8080/v1/health   # {"status":"ok",...} — process is up
   curl localhost:8080/v1/ready    # reports store health, doesn't gate on it
   ```
   Talking point: readiness never fails just because a store is down — the
   broker still hands out creds; a red store fails *the consumer's*
   connection, not this process.

3. **AuthN (30s).**
   ```bash
   curl -i localhost:8080/v1/capabilities                                  # 401
   curl localhost:8080/v1/capabilities -H "X-API-Key: air-classifier-dev"  # 200
   ```

4. **The core flow — issue real credentials and use them (3 min, the money shot).**
   ```bash
   curl -X POST localhost:8080/v1/resources/postgres/credentials \
     -H "X-API-Key: air-classifier-dev" -H "Content-Type: application/json" -d '{}'
   # {"resource":"postgres","endpoint":{"host":"pgbouncer","port":6432,"db":"air"},
   #  "username":"air","password":"localdevpass", ...}
   ```
   Then actually connect with what came back:
   ```bash
   PGPASSWORD=localdevpass psql -h localhost -p 6432 -U air -d air -c 'select 1;'
   ```
   This is the point: the endpoint handed back is a *real, connectable*
   endpoint, not a placeholder.

5. **Policy scoping (2 min).**
   ```bash
   curl localhost:8080/v1/resources -H "X-API-Key: air-classifier-dev"
   curl localhost:8080/v1/resources -H "X-API-Key: air-client-dev"
   ```
   Same endpoint, different service token, different visible resource
   list — driven entirely by [`deploy/policy/policy.local.yaml`](../deploy/policy/policy.local.yaml),
   no code change.

6. **The client SDK (1 min).** Show [`client/client.go`](../client/client.go) —
   `c.PostgresDSN(ctx)` does everything step 4 did by hand, and returns a DSN
   the caller hands to its own driver.

7. **Observability (1 min).**
   ```bash
   curl localhost:8080/metrics | grep air_infrastructure_service_credentials_issued_total
   ```
   Point at Grafana/Prometheus if `make up-observability` is running.

8. **The shared-network trick (1 min, if a sibling is running).** `make doctor`
   lists which sibling containers have joined `air-net` — the "one command,
   whole platform" property.

### If you only have 5 minutes

Steps 1, 2, 4, 5 — skip auth/SDK/metrics detail. The credential-issuance →
real-connection moment (step 4) is the one thing worth not cutting.

---

## Current Gaps

Honest limitations as of this writing — not hidden, because DevOps and future
maintainers need to know what's load-bearing vs. what's a local convenience.

- **Rate limiting does not run at all.** Worth reading twice: the limiter is
  constructed at startup and `EnforceRate` exists, but **no route ever calls
  it**. `rate_per_minute` in the policy document is inert and
  `air_infrastructure_service_ratelimited_total` never leaves zero. Turning
  enforcement on would start rejecting sibling traffic that is accepted today,
  so it is a decision for the team rather than a loose end to tidy — but the
  current state, configured and documented and inert, is the worst of the
  options. Wiring it into `issueCredentials` is a two-line change.
  Separately, and only once it is wired: the bucket in
  [`internal/policy/ratelimit.go`](../internal/policy/ratelimit.go) is
  per-replica, so it would need a Redis-backed implementation to mean anything
  with more than one replica.
- **Credentials never expire.** The broker always returns `expires_at: null` —
  no lease or rotation. Acceptable for a local dev credential; not a pattern to
  copy into anything that *does* run in prod.
- **Only the `env` secret backend is real.** `FileSecretBackend` is implemented
  but has no exercised config (`deploy/secrets/secrets.json` doesn't exist here).
  `VaultSecretBackend` returns `ErrVaultNotImplemented` — a deliberate fill-in
  point, shaped so filling it in is a config swap rather than a new seam.
- **Admin API is read-only.** Registering a new resource or editing a grant
  today means hand-editing YAML and restarting, not an API call.
- **No durable audit trail.** Credential issuance and policy decisions are
  structured-logged, not written to any queryable audit store.
- **`service` is the only principal.** `Principal` carries a `tenant` field so
  quotas and audit can split later, but nothing populates it.
- **Policy is boot-time only.** Editing `policy.local.yaml` requires a restart —
  no file-watch or hot-reload.
- **No mTLS / stronger service identity.** Auth is a static shared token per
  service, configured via `.env`; there's no rotation or per-request identity
  beyond the token itself.

---

## Future Extensions / Roadmap

Roughly in order of "someone would actually reach for this next":

1. **Decide about rate limiting.** Either wire `EnforceRate` into
   `issueCredentials` and make `rate_per_minute` mean something, or delete the
   limiter, the metric and the policy field so the document stops advertising a
   control that does not exist. The current state — configured, documented,
   inert — is the worst of the three, and it is the first thing a new owner
   should resolve.
2. **Admin write endpoints.** Turn the registry/policy admin surface from
   read-only into create/update, so a new resource or grant is an API call
   instead of a YAML edit + restart.
3. **Hot-reloadable policy.** Watch `policy.local.yaml` and rebuild the engine
   without a restart — meaningfully faster local iteration when tuning grants
   during a demo or a debugging session. Cheap here: the engine is immutable
   after load, so reloading is swapping a pointer.
4. **`VaultSecretBackend` implementation.** The interface and the fill-in point
   already exist; this is scoped, contained work whenever a team actually needs
   dynamic or leased credentials locally.
5. **Tenant field on `Principal`.** Carry a `tenant` through the request context
   so quotas and audit can split below service level without an API-breaking
   change.
6. **Redis-backed rate limiter**, if this ever runs as more than one replica.
   The `Check` method is already the seam for a drop-in swap.
7. **Durable audit sink.** Credential issuance and policy decisions currently
   only reach structured logs; a queryable sink (even a local Postgres table)
   would make "who touched what" answerable without grepping container logs.
8. **CI.** `make check` on every push, and `make up && make doctor` on a
   schedule, would catch the stack breaking before a developer does.

Anyone picking this project up: start with the [Gaps](#current-gaps) section
above, cross-reference against what's actually still true in the code (this
doc rots the same way any doc does), and pick from the roadmap based on what
the team actually needs next — none of it is committed, all of it is options.
