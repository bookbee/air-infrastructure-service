# air-infrastructure-service

Local dev infrastructure for the AIR platform: a shared docker-compose stack
(Redis, Postgres, MongoDB) plus a small control-plane API that brokers scoped
credentials to those stores. It exists so every AIR service dev-time resolves
against the same instances instead of standing up its own.

- Setting up, and using it from your service →
  **[`docs/01-setup.md`](docs/01-setup.md)**
- Architecture, design decisions, gaps, roadmap →
  [`docs/overview.md`](docs/overview.md)

This repo is **local-only**. In deployed environments, DevOps provides Redis,
Postgres, Mongo, and observability directly — this service does not run there.

Model inference lives in [`air-llm`](../air-llm) (port `8083`, its own
standalone compose stack); it is not part of this repo.

## Quick start

```bash
make env            # .env from .env.example
make up             # redis, postgres, pgbouncer, mongo, this service
make doctor         # verify the stack is serving what sibling services expect
curl localhost:8080/v1/health
```

Optional add-ons: `make up-tools` (adminer / mongo-express / redisinsight),
`make up-observability` (prometheus / grafana). `make help` lists every target.

To work on the service itself, no Docker is needed:

```bash
make check          # lint + vet + test  ← the gate before calling work done
make run            # serve on :8080 against the stack's published ports
```

## How it works

Apps don't connect to Redis/Postgres/Mongo directly with hardcoded endpoints —
they ask this service for scoped credentials and connect themselves:

```text
app: client.PostgresDSN(ctx)
   └─▶ POST /v1/resources/postgres/credentials   (X-API-Key = service token)
          authN → policy(service, postgres, connect) → broker
          ◀── { host: pgbouncer, port: 6432, db, user, password }
   └─▶ app opens its own pooled connection directly to pgbouncer
```

The broker hands out the endpoint and steps aside — it is never on the data
path, so a store outage fails the consumer's own connection, not this process.
That's also why `/v1/ready` reports store health without gating on it (see
[`internal/api/v1/system.go`](internal/api/v1/system.go)).

Components:

| Component | Responsibility |
|---|---|
| **Registry** ([internal/registry/](internal/registry/)) | Resource name → kind → per-environment endpoint |
| **Broker** ([internal/broker/](internal/broker/)) | Turns "I want resource X" into a scoped credential, via a pluggable secret backend |
| **Policy engine** ([internal/policy/](internal/policy/)) | RBAC (service × resource × action) + rate limits |
| **Client SDK** ([client/](client/)) | Consumer-side: fetch a grant, return a ready connection string |

## API

| Route | Auth | Purpose |
|---|---|---|
| `GET /v1/health` | none | Liveness. Never consults a dependency |
| `GET /v1/ready` | none | Readiness. **Reports** store health, never gates on it |
| `GET /v1/capabilities` | `X-API-Key` | Version, environment, resource kinds |
| `GET /v1/resources` | `X-API-Key` | Resources the caller is granted, per policy |
| `POST /v1/resources/{name}/credentials` | `X-API-Key` | Issue a scoped credential grant |
| `GET /v1/admin/registry` | `X-API-Key` + `admin_read` | Dump the registry |
| `GET /v1/admin/policy` | `X-API-Key` + `admin_read` | Dump the policy document |
| `GET /metrics` | none | Prometheus exposition |

Failures are RFC 9457 problem details (`application/problem+json`) with a stable
`code`: `unauthorized`, `policy_denied`, `resource_not_found`, `rate_limited`,
`dependency_unavailable`, `validation_error`, `internal_error`. Every response
carries an `X-Request-ID`, echoed from the request when the caller supplies one.

## The shared local stack

Sibling repos do not run their own Redis, Postgres or Mongo — they run only
their own process and point at these. Services reach it by joining the
`air-net` network, which this compose file owns under a fixed name:

```yaml
# in a sibling service's docker-compose.yml
networks:
  default:
    name: air-net
    external: true
```

Their containers then resolve `redis`, `postgres`, `pgbouncer`, `mongo` and
`air-infrastructure-service` by service name, with no per-service network wiring.
`external: true` means **this stack must be up first** — a sibling started
against a missing network fails immediately, rather than coming up healthy
and timing out on every call.

Running a service natively (its own `make run`) instead of as a container
works too: everything here publishes its port on the host, so
`localhost:6379` and friends address the same instances.

## Port map

**This table is the platform's canonical port map.** Every AIR service is
expected to run at once on one laptop, so host ports are allocated here rather
than defaulted per repo — including for services (like `air-llm`) that don't
join this stack.

| Port | Owner | Notes |
|---|---|---|
| 8080 | **air-infrastructure-service** | Broker / control plane (this repo) |
| 8081 | air-orchestrator-service | Conversational API |
| 8082 | air-classifier-service | `/v1/sentiment`, `/v1/feedback`, `/v1/reviews` |
| 8083 | air-llm | Model gateway — standalone, own compose stack, reached directly |
| 8084 | air-tools | |
| 8085 | air-action | Not yet built |
| 8086 | air-recommender | Not yet built |
| 8087 | air-rag | |
| 8088 | air-adapter | ECP adapter (Node) |
| 8501 | air-client | Streamlit console |
| 8090 / 8091 / 8092 | adminer / mongo-express / redisinsight | `make up-tools` |
| 9090 / 3000 | prometheus / grafana | `make up-observability` |
| 6379 / 5432 / 6432 / 27017 | redis / postgres / pgbouncer / mongo | Native ports |

A new service claims the next free port in the block and adds a row here in
the same commit. Each service's port is overridable (`make run PORT=…`).

Metrics work the same way: a service exposes `/metrics` and registers a
scrape target in [`deploy/prometheus/prometheus.yml`](deploy/prometheus/prometheus.yml),
rather than shipping a Prometheus of its own.

## Consuming it

Over HTTP, from any language — that is the supported contract, and what every
sibling does today:

```bash
curl -X POST localhost:8080/v1/resources/postgres/credentials \
  -H "X-API-Key: air-classifier-dev" -H "Content-Type: application/json" -d '{}'
```

From Go, [`client/`](client/) wraps the same call. It returns a connection
string rather than a live driver client, so importing it pulls in no drivers
and pins no versions:

```go
c := client.New("", "")                  // reads AIR_INFRASTRUCTURE_SERVICE_URL/_TOKEN
dsn, err := c.PostgresDSN(ctx)           // postgres://air:...@pgbouncer:6432/air
redisURL, err := c.RedisURL(ctx)
mongoURI, err := c.MongoURI(ctx)
```

Grants are cached until they expire; a static local grant is fetched once.

## Configuration

All settings are `AIR_INFRASTRUCTURE_SERVICE__*` environment variables — see
[`.env.example`](.env.example) for the full list. Nested keys use a
double-underscore delimiter, e.g. `AIR_INFRASTRUCTURE_SERVICE__REDIS__URL`. Precedence is
process environment, then `.env`, then the built-in default; an unparseable
value fails the boot rather than silently applying a default.

Access policy (which service may reach which resource) is config, not code — see
[`deploy/policy/policy.local.yaml`](deploy/policy/policy.local.yaml). It is read
at boot, so a change needs a restart.

**`rate_per_minute` in that document is currently inert.** The limiter is built
and the enforcement helper exists, but no route calls it. See
[`docs/overview.md`](docs/overview.md) "Current Gaps"; resolving it is the first
item on the roadmap.

## Building and running outside compose

```bash
make build                                  # -> bin/air-infrastructure-service
./bin/air-infrastructure-service            # reads ./.env if present
./bin/air-infrastructure-service -version
./bin/air-infrastructure-service -healthcheck   # what the container HEALTHCHECK runs
```

The runtime image is `gcr.io/distroless/static-debian12:nonroot`: a static
binary, the policy document, and nothing else — no shell, no package manager,
no interpreter. It runs as uid 65532 and needs no writable filesystem. Two
direct dependencies (`prometheus/client_golang`, `gopkg.in/yaml.v3`); the rest
is the standard library.

## Everyday commands

```bash
make help            # list every target
make up / down       # start / stop the stack
make doctor          # verify the stack is serving what sibling services expect
make logs SVC=redis  # tail one service's logs
make check           # lint + vet + test
make cover           # tests with a coverage summary
```
