# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

The AIR platform's local control plane: a Go service on **:8080** that brokers scoped,
policy-checked credentials for Redis, Postgres and Mongo, plus the docker-compose stack
that hosts them for the whole estate. It is **local-only** and never deployed — in real
environments DevOps provisions those stores directly.

The repository also holds, in `gke/`, **the cloud deployment code for the five core
services** (two Helm charts, values per service per environment, Terraform, scripts, a
kind rehearsal). That code has nothing to do with the Go service: it is here because
DevOps owns this repository and wants one place to deploy from. `gke/README.md` is the
map and `docs/03-gke-runbook.md` the procedure. `make gke-validate` is its gate, and
`make kind-up kind-test` its runtime proof; run both after changing anything under
`gke/`. Each core repository's `make deploy` calls back into `make gke-deploy` here,
so the target names and their variables (`SERVICE`, `DEPLOY_ENV`, `KUBE_CONTEXT`,
`IMAGE_REPOSITORY`, `IMAGE_DIGEST`, `IMAGE_TAG`, `GCP_PROJECT`, `GATEWAY_DIR`,
`KEYS_FILE`) are a contract with five other Makefiles.

DevOps owns this repo. The audience for every README, Makefile target and comment is an
operator, not the author.

## The platform contract

These are load-bearing for the rest of the estate. Changing any of them is a
platform-wide change, not a local decision:

| Surface | Must stay |
|---|---|
| HTTP routes | `GET /v1/health`, `GET /v1/ready`, `GET /v1/capabilities`, `GET /v1/resources`, `POST /v1/resources/{name}/credentials`, `GET /v1/admin/registry`, `GET /v1/admin/policy`, `GET /metrics` |
| Response bodies | Field names, types, and nesting identical — including `application/problem+json` errors (`type`/`title`/`status`/`code`/`detail`/`request_id`) and the exact `code` strings (`unauthorized`, `policy_denied`, `resource_not_found`, `rate_limited`, `dependency_unavailable`, `validation_error`, `internal_error`) |
| Auth | `X-API-Key` header → service principal, from `token:service` pairs |
| Env vars | `AIR_INFRASTRUCTURE_SERVICE__*` with `__` for nesting — see `.env.example` |
| Metric names | `air_infrastructure_service_{requests,credentials_issued,ratelimited}_total` — Prometheus scrapes these by name; a rename breaks any dashboard built on them |
| Metric `route` label | The path template **without** the `/v1` prefix (`/resources/{name}/credentials`); an unmatched path uses its full raw path. Dashboards select on this value — see `routeLabel` in `internal/api/middleware.go` |
| Policy YAML | `deploy/policy/policy.local.yaml` schema unchanged; the file is DevOps-owned config, not code |
| Ports | `8080` published, `8080` in-container |
| Compose network | `air-net`, under that fixed name. Four siblings declare it `external: true`, so renaming it breaks every one of them. The project and service names are this repo's own and safe to change; `air-net` is the platform's |
| `X-Request-ID` | Echoed back; generated when absent |

### Two behaviours that look like bugs and are not

Both are covered by tests. Don't "fix" either without reading the reasoning.

- **`/v1/ready` returns 200 with stores down.** It *reports* store health, never gates on
  it. The broker hands out an endpoint and steps aside, so a red store fails the
  consumer's connection, not this process — gating would pull the broker out of rotation
  at the exact moment a consumer needs fresh credentials.
- **Resource listing visibility ignores `default_allow`.** `can_see` follows explicit
  grants only, so a listing reflects what a service is *declared* to use even in a
  local default-allow environment.

A third is a known gap, not a bug to tidy: **rate limiting is built but never called.**
`Deps.EnforceRate` exists and nothing invokes it, so `rate_per_minute` is inert and
`..._ratelimited_total` stays at zero. Wiring it in would start rejecting sibling traffic
that is accepted today, so it is a team decision (`docs/overview.md` → "Current Gaps").

## Commands

`make help` lists every target. `make check` (lint + vet + test) is the pre-"done" gate;
`make doctor` is its runtime equivalent and is the one to trust after `make up`.

`make lint` falls back to gofmt + staticcheck when golangci-lint is absent, and looks in
`GOPATH/bin` as well as `PATH`.

- Single test: `go test ./internal/app -run TestReadyWhenStoresAreDown -v`
- `make cover` uses `-coverpkg=./...` because the HTTP handlers (`internal/api/v1`) have
  no tests of their own. They are tested end-to-end from `internal/app/app_test.go`.
- `make run` runs natively against the compose stack's published ports. It overrides the
  store addresses from `.env`, which use compose service names that don't resolve from
  the host.
- `.claude/settings.json` denies `git push`, `gh pr create`, `make nuke` and volume
  deletion. Leave those to the user.

## Architecture

The wiring is easy to miss because it is spread across a few files:

- **Boot** (`internal/app/app.go` `New`): settings → registry, policy, secret backend,
  metrics → one `api.Deps`. A bad policy file or missing secret file fails here, at boot,
  not on the first request. `Handler(deps)` is split out from `New` so tests can inject
  their own `Deps`. `Deps.Probe` is a seam for store health, so readiness tests need no
  live stores.
- **Request path**: `api.Observe` (`internal/api/middleware.go`) wraps the *whole* mux,
  so unmatched paths are also counted and logged. It sets the request ID, the
  request-scoped logger, the metric and the access log. Routes are stdlib `ServeMux`
  method patterns (`internal/api/v1/router.go`). Each one goes through `Deps.Handle`,
  which turns a returned error (or a panic) into problem+json. The catch-all `/` serves
  a JSON 404, not the mux's plain-text one.
- **Credential issue**: `issueCredentials` → `Deps.RequirePrincipal` (X-API-Key) →
  `broker.Issue` (registry lookup → `policy.Decide(..., connect)` → `SecretBackend.Get`).
  The handler maps broker errors with `errors.Is`: `ErrResourceNotFound`→404,
  `ErrPolicyDenied`→403, anything else→`dependency_unavailable`.
- **Secret backends** (`internal/broker/secrets.go`): `env` is the real one, `file` is
  implemented but has no config, and `vault` is a stub that always errors. Choose one
  with `SECURITY__SECRET_BACKEND`.
- **Config** (`internal/config`): env vars override the `.env` file, which is read by a
  small in-repo parser (`dotenv.go`), not a library. Invalid enum values refuse to boot.

**This is a Go port of an earlier Python service**, and its outward behaviour must match
that service: 404 bodies, metric labels, request-ID shape, how tokens are parsed. Code
comments that say "matching the Python" mark these parity constraints. They are not
open to "cleaner" redesigns.

## Go conventions in this repo

- **Two direct dependencies** (`prometheus/client_golang`, `gopkg.in/yaml.v3`). Every
  dependency is something DevOps inherits and patches; routing, config, JSON, logging
  and probes are all stdlib. Add a third only with a reason worth writing down.
- **Handlers return `error`.** `api.HandlerFunc` funnels every failure through
  `WriteProblem`, so no handler invents a response shape.
- **Wrap sentinels, never concatenate them.** `fmt.Errorf("%s: %w", detail, ErrX)` puts
  the sentinel's text in the `detail` a caller sees, which has bitten this code before.
  Carry a typed error with a clean `Detail` and an `Unwrap`.
- **Pointers in `internal/schema` are deliberate.** Those fields are `*T` with no
  `omitempty` so an absent value serialises as `null` rather than vanishing: a `nil`
  `Username` on a URI-auth store means "inapplicable", not "empty".
- **`internal/` for everything the platform does not import**; `client/` is the only
  exported package.

## Platform context

Sibling repos sit next to this one, in `../` (opened together through
`../air-platform.code-workspace`). Each is an independent HTTP service; they call each
other over HTTP and are **never imported as packages**.

This service is **local-only** and never deployed. In real environments DevOps provisions
Redis/Postgres/Mongo/observability directly. Keep that property — it is why the compose
file, not the binary, is most of the local product. (`gke/` deploys the *other* services;
it has no chart for this one, on purpose.)

### Port map

`docs/02-gcp-deployment.md` is the DevOps guide to what replaces this stack in GCP
and how each AIR service's image is built and deployed. It describes the other
repositories, so update it when a service's port, settings prefix, dependencies or
scaling constraints change.

**This repo's `README.md` holds the platform's canonical port map.** A new service claims
the next free port in the 8080–8089 block and adds its row there in the same commit —
don't start a second copy here.

### The `air-net` contract

This repo's compose file owns the shared stack and declares the network under a fixed
name. Siblings join it with exactly:

```yaml
networks:
  default:
    name: air-net
    external: true
```

`external: true` is deliberate: a sibling started before this stack fails loudly
("network air-net not found") instead of booting healthy and timing out on every call.
`air-model-gateway` (called `air-llm` in older docs, including this README's port map) is
the exception. It runs its own stack on `air-model-gateway-net` and is reached over
`host.docker.internal:8083`.

### Estate conventions worth matching

- **Config is env vars** with a service prefix and `__` for nesting; `.env.example` is
  committed, `make env` copies it, the real `.env` is untracked.
- **Compose files carry real prose** — every sibling's opens with a comment block saying
  what it starts, what it deliberately does *not* start, and the ordered commands to bring
  dependencies up first.
- **Health vs. ready are distinct.** Container healthchecks probe `/v1/health` only.
- **Metrics are centralised** — expose `/metrics`, register a scrape target in
  `deploy/prometheus/prometheus.yml`; do not ship a second Prometheus.
- **`docs/overview.md`** exists in every `air-*` repo with identical H2 headings, so
  `air-client` can walk the siblings and assemble one cross-project presentation. Keep
  this repo's headings byte-identical to the others.
