# air-infrastructure-service

The AIR platform's infrastructure repository, owned by DevOps. It holds two separate
things:

1. **The local development stack**: one Docker Compose stack (Redis, Postgres,
   MongoDB) that every AIR service shares on a developer's machine, plus a small Go
   service that hands out scoped credentials to those stores. Local only.
2. **The cloud deployment code**, in [`gke/`](gke/README.md): the Helm charts,
   Terraform and scripts that deploy the five core services (air-model-gateway,
   air-classifier-service, air-rag-service, air-tools-gateway,
   air-orchestrator-service) to UAT and production on GKE.

> **Part of the AIR platform.** New here? Start with the
> [platform overview](../air-web-console/docs/platform/01-overview.md) and the
> [local setup guide](../air-web-console/docs/platform/03-local-setup.md) in
> air-web-console, which bring up all seven repositories together.

| If you are… | Read |
| --- | --- |
| A developer who needs the shared stack running | [Quick start](#quick-start), then [`docs/01-setup.md`](docs/01-setup.md) |
| **DevOps, deploying AIR to GCP** | **[`docs/02-gcp-deployment.md`](docs/02-gcp-deployment.md)** (what replaces the local stack in GCP and what each service needs), then **[`docs/03-gke-runbook.md`](docs/03-gke-runbook.md)** (the procedure) and [`gke/README.md`](gke/README.md) (the map of the deployment code) |
| Changing this service | [`docs/overview.md`](docs/overview.md) (architecture, decisions, gaps), then [`CLAUDE.md`](CLAUDE.md) |

The Go service and the Compose stack are **local-only**: in a deployed environment,
managed services (Memorystore, Secret Manager) take their place and this service does
not run. The repository is also called `air-infra` in older documents and in other
repositories' comments.

Model access is a separate service, [air-model-gateway](../air-model-gateway) (port
`8083`, called `air-llm` in older documents). It runs its own Compose stack and is
not part of this repository.

## Quick start

Needs Docker Desktop (or Docker Engine with Compose v2), running.

```bash
make env            # .env from .env.example — do this first
make up             # redis, postgres, pgbouncer, mongo, this service
make doctor         # verify the stack is serving what sibling services expect
curl localhost:8080/v1/health
```

**Run `make env` before `make up`.** Without a `.env` the service starts, but with
no service tokens and the wrong store addresses: every call is a 401 and `/v1/ready`
reports every store `down` (air-web-console bug IN-001). `make doctor` catches it.

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
than defaulted per repository, including for air-model-gateway, which doesn't
join this stack. These are **host** ports: inside its container every service
listens on 8080, except the orchestrator (8081) and the console (3000).

| Port | Owner | Notes |
|---|---|---|
| 8080 | **air-infrastructure-service** | Credential broker (this repository) |
| 8081 | air-orchestrator-service | The conversational API: `/v1/chat`, `/v1/query` |
| 8082 | air-classifier-service | `/v1/classify`, `/v1/classify/batch`, `/v1/summary/refresh` |
| 8083 | air-model-gateway | The model gateway: `/v1/generate`, `/v1/embed`. Own Compose stack, reached over the host |
| 8084 | air-tools-gateway | `/v1/tools/{name}`, `/v1/actions/{name}/propose` and `/execute` |
| 8085 | (reserved) | `air-action` was merged into air-tools-gateway and will not be built |
| 8086 | (reserved) | `air-recommender`, not started |
| 8087 | air-rag-service | `/v1/retrieve` |
| 3000 | air-web-console | The test console. **Also Grafana's port below**: don't run both, or start the console with `make dev PORT=3001` |
| 8090 / 8091 / 8092 | adminer / mongo-express / redisinsight | `make up-tools` |
| 9090 / 3000 | prometheus / grafana | `make up-observability` |
| 6379 / 6432 / 27017 | redis / pgbouncer / mongo | Published on the host. Postgres itself (5432) is reachable only through pgbouncer |
| 11434 | Ollama | Not part of this stack: the local models, run natively on the host for air-model-gateway |

A new service claims the next free port in the block and adds a row here in
the same commit. Each service's port is overridable (`make run PORT=…`).

Metrics work the same way: a service exposes `/metrics` and registers a
scrape target in [`deploy/prometheus/prometheus.yml`](deploy/prometheus/prometheus.yml),
rather than shipping a Prometheus of its own. **Today that file scrapes only this
service and the classifier**; the other four services expose `/metrics` and still
need their jobs added (air-web-console bug IN-002).

### Who uses what today

| Store | Used by | For |
|---|---|---|
| Redis | air-classifier-service | Verdict cache, idempotency |
| Redis | air-tools-gateway | Proposals, idempotency records, locks (keys under `airtg:`) |
| Redis | air-model-gateway, only with `REDIS=infra` | Shared cache, rate limits, spend |
| MongoDB | air-classifier-service, only with `AUDIT__BACKEND=mongo` | Optional audit log |
| Postgres | nothing yet | Reserved for later phases |
| The broker API | air-orchestrator-service | A health probe only |

The services above reach Redis with the address and password in their own Compose
files. None of them fetches credentials from the broker yet: the broker is in place
for when they do. That is why its token and policy names (`air-classifier`,
`air-platform`, `air-client`) still carry the services' earlier names.

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

## Deploying the AIR services to GKE

The deployment code for every core service is in [`gke/`](gke/README.md):

```bash
make gke-validate        # lint the charts, schema-check every manifest, validate Terraform
make kind-up             # all five services on a local Kubernetes cluster, production values
make kind-test           # smoke-test them, end to end
make gke-deploy SERVICE=air-rag-service DEPLOY_ENV=uat KUBE_CONTEXT=… GCP_PROJECT=… \
     IMAGE_REPOSITORY=… IMAGE_DIGEST=sha256:…
```

Each service's repository keeps its own `Dockerfile` and has `make image-push`,
`make deploy` and `make deploy-smoke`, which build the image there and call the
targets above. [`docs/03-gke-runbook.md`](docs/03-gke-runbook.md) is the full
procedure.

## Everyday commands

```bash
make help            # list every target
make up / down       # start / stop the stack
make doctor          # verify the stack is serving what sibling services expect
make logs SVC=redis  # tail one service's logs
make check           # lint + vet + test
make cover           # tests with a coverage summary
```
