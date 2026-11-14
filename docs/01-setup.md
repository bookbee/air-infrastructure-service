# Setup

For **AIR platform developers**: how to run the shared local stack on your
machine, and how your service talks to it.

This repo owns Redis, Postgres, MongoDB and the credential broker for the whole
platform's local development. You do not run your own copies of those — you
start this stack once and point at it.

---

## 1. First-time setup

Prerequisites: Docker Desktop running. Go 1.25 only if you want to work on the
service itself — running it needs nothing but Docker.

```bash
git clone https://github.com/bookbee/air-infrastructure-service.git
cd air-infrastructure-service
make env      # writes .env from .env.example — no edits needed
make up       # redis, postgres, pgbouncer, mongo, and this service on :8080
make doctor   # every check should be green
```

`make doctor` is the one to trust. It verifies the daemon, the `air-net`
network, all four stores, this service's liveness and readiness, that it
**actually brokers a working credential**, and that siblings can resolve it as
`air-infrastructure-service:8080` from inside the network.

Optional: `make up-tools` (adminer :8090, mongo-express :8091, redisinsight
:8092) and `make up-observability` (prometheus :9090, grafana :3000).

Then start whatever sibling services you need — they join `air-net`, which this
stack owns and must therefore be up first.

---

## 2. Using it from your service

Over HTTP, from any language:

```bash
curl -X POST localhost:8080/v1/resources/postgres/credentials \
  -H "X-API-Key: air-classifier-dev" -H "Content-Type: application/json" -d '{}'
# {"resource":"postgres","kind":"postgres",
#  "endpoint":{"db":"air","host":"pgbouncer","port":6432},
#  "username":"air","password":"localdevpass","uri":null,"expires_at":null}
```

Use the endpoint and credentials to open your **own** connection. This service
is never on the data path, so it is not a bottleneck and restarting it does not
drop your connections.

Addressing depends on where your code runs:

| Your service runs | Base URL |
|---|---|
| In a container on `air-net` | `http://air-infrastructure-service:8080` |
| Natively on your laptop | `http://localhost:8080` |

Go consumers can use [`client/`](../client/client.go), which wraps the call and
returns a connection string rather than a live driver client — so importing it
pins no driver versions:

```go
c := client.New("", "")            // reads AIR_INFRASTRUCTURE_SERVICE_URL/_TOKEN
dsn, err := c.PostgresDSN(ctx)     // postgres://air:localdevpass@pgbouncer:6432/air
```

### Joining `air-net`

Your service's `docker-compose.yml` needs exactly these three lines:

```yaml
networks:
  default:
    name: air-net
    external: true
```

`external: true` is deliberate: a sibling started before this stack fails
loudly ("network air-net not found") instead of coming up healthy and timing
out on every call.

`air-llm` is the exception — its own stack on its own network, reached over
`host.docker.internal:8083`.

### Getting a token for a new service

Two edits, both config:

1. Add `yourtoken:your-service` to
   `AIR_INFRASTRUCTURE_SERVICE__SECURITY__SERVICE_TOKENS` in `.env`
   (comma-separated `token:service` pairs).
2. Add a grant block to [`deploy/policy/policy.local.yaml`](../deploy/policy/policy.local.yaml).

Then `make up` to restart — tokens and policy are read at boot, not watched.

### Claiming a port

The platform's port map lives in this repo's [`README.md`](../README.md). A new
service claims the next free port in the 8080–8089 block and adds its row there
in the same commit, so one file stays the answer to "what is already taken".

---

## 3. Troubleshooting

**`network air-net not found` when starting a sibling.**
This stack owns that network and must be up first: `make up` here. The failure
is deliberately loud — a sibling that started without it would come up healthy
and then time out on every call.

**Port 8080 already in use.**
Something else claimed the broker's slot. `docker ps --filter publish=8080`, or
`lsof -iTCP:8080 -sTCP:LISTEN`.

**401 with a token that used to work.**
Tokens come from `AIR_INFRASTRUCTURE_SERVICE__SECURITY__SERVICE_TOKENS` in
`.env`. If you created `.env` before a change to `.env.example`, diff them —
`make env` will not overwrite an existing file.

**403 `policy_denied`.**
Your service has no grant on that resource in `policy.local.yaml`. Note that
`GET /v1/resources` lists only what you are explicitly granted, which is the
quickest way to see what you actually have.

**`redis-cli -u` says `NOAUTH Authentication required`.**
A `redis-cli` quirk with the `redis://:password@host` form the grant returns
(empty username). The URI is correct — real client libraries accept it. For
`redis-cli`, use `-h redis -a localdevpass`, or the explicit
`redis://default:localdevpass@redis:6379/0`.

**Rate limits are not being applied.**
Correct — they are not implemented. `rate_per_minute` in the policy document is
currently inert. See [`overview.md`](overview.md) "Current Gaps".
