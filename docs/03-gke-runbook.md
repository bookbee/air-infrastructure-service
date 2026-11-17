# GKE runbook: deploying the AIR services to UAT and production

For **DevOps**. The step-by-step procedure for standing up an environment on Google
Cloud and deploying the five core services to it: air-model-gateway,
air-classifier-service, air-rag-service, air-tools-gateway and
air-orchestrator-service.

Everything used here is in this repository, under [`gke/`](../gke/README.md).
[02-gcp-deployment.md](02-gcp-deployment.md) is the companion that explains *why*:
what each service needs and what replaces the local stack. Read it once first.

> **What has been proven, and where (2026-11-17).** All five services were deployed
> with these charts and production values to a local `kind` cluster and passed their
> smoke tests, including a real conversation turn with a cited answer
> ([section 9](#9-what-has-been-verified-and-what-hasnt)). **Nothing here has been
> applied to a real GCP project yet.** The first UAT rollout is where Terraform, the
> Secret Manager add-on, Memorystore and Autopilot get proven; this runbook is written
> to make that a single pass.

- [1. Target shape](#1-target-shape)
- [2. What is where](#2-what-is-where)
- [3. Prerequisites](#3-prerequisites)
- [4. Rehearse on a laptop first](#4-rehearse-on-a-laptop-first)
- [5. First-time setup of an environment](#5-first-time-setup-of-an-environment)
- [6. Deploying a service](#6-deploying-a-service)
- [7. Day-2 operations](#7-day-2-operations)
- [8. Security posture](#8-security-posture)
- [9. What has been verified, and what hasn't](#9-what-has-been-verified-and-what-hasnt)

## 1. Target shape

```
                 GKE cluster (Autopilot, private nodes), one per environment
 ┌─────────────────────────────────────────────────────────────────────────┐
 │  front door: internal Gateway (gke-l7-rilb), private IP, HTTPS,         │
 │  Cloud Armor rate limit. Only the shop's backend calls it (5.6)         │
 │        │                                                                │
 │        ▼                                                                │
 │  ns air-orchestrator-service ── 1 pod, Recreate                         │
 │        │            │              │                 │                  │
 │        ▼            ▼              ▼                 ▼                  │
 │  ns air-classifier  ns air-rag     ns air-tools      ns air-model-gateway│
 │     -service           -service       -gateway          2–10 pods (HPA)  │
 │     2–6 pods (HPA)     1 pod          2–4 pods (HPA)        ▲            │
 │        │               │                                    │            │
 │        └───────────────┴────────────────────────────────────┘            │
 │                                                          │              │
 │  each namespace: Deployment, Service (ClusterIP),        ├─► ns air-model-servers
 │  NetworkPolicy, ServiceAccount, SecretProviderClass,     │   (Ollama / vLLM; owned
 │  PodMonitoring                                           │    separately)
 └──────────────────────────────────────────────────────────┼──────────────┘
        │ secrets: Secret Manager add-on (CSI) + Workload Identity
        ├──► Memorystore Redis (AUTH + TLS, private IP)
        └──► Cloud NAT ──► Gemini, Anthropic, OpenAI APIs   (gateway only)
```

- **One project per environment** (UAT, prod). Secret names are fixed, so two
  environments can't share a project.
- **One namespace per service**, named after the service. The Helm release, the
  Deployment, the Service and the service account carry the same name.
- **In-cluster, with one private front door.** Services call each other at
  `http://<service>.<service>.svc.cluster.local` with an API key in `X-API-Key`.
  The only way in from outside the cluster is the orchestrator's front door: an
  *internal* load balancer with a private IP in the VPC, which only the shop's
  backend calls. Nothing is on the public internet
  ([section 5.6](#56-the-orchestrators-front-door)).
- **Model servers are not part of this deployment.** The gateway calls them; it
  doesn't host them. Until they exist, the gateway's local rungs fail fast and chat
  falls over to cloud models, **but air-rag-service cannot work**: its embedding
  model (`local-768-v1`) is served by a model server only.

### Environment names

Deployments are named **`uat`** and **`prod`**. Three services have their own word
for the pre-production environment, and it is not `uat`:

| Service | Its setting | Value in UAT | Value in prod |
| --- | --- | --- | --- |
| air-model-gateway | `AIR_MODEL_GATEWAY_ENVIRONMENT` (also selects `deploy/policy/policy.<value>.yaml`) | `staging` | `prod` |
| air-classifier-service | `AIR_CLASSIFIER__APP__ENV` | `staging` | `production` |
| air-rag-service | `AIR_RAG_SERVICE__APP__ENV` | `staging` | `production` |
| air-orchestrator-service | `AIR_ORCHESTRATOR_SERVICE__APP__ENV` | `staging` | `production` |
| air-tools-gateway | `AIR_TOOLS_GATEWAY__APP__ENV` | `production` (it has no `staging`) | `production` |

The values files already set these. You only type `uat` or `prod`.

## 2. What is where

In this repository:

| Path | What it is | When you touch it |
| --- | --- | --- |
| `gke/helm/air-model-gateway/` | The gateway's chart | Rarely |
| `gke/helm/air-service/` | The shared chart for the other four services | Rarely |
| `gke/values/<uat\|prod>/<service>.yaml` | One values file per service per environment | Fill the `<placeholders>` once; edit settings, replicas, allowed namespaces |
| `gke/values/kind/<service>.yaml` | The laptop rehearsal's overrides | Rarely |
| `gke/terraform/environments/<uat\|prod>/` | The environment's Terraform root | `terraform apply` once; re-apply on change |
| `gke/terraform/modules/gke-platform/` | VPC, Cloud NAT, Autopilot cluster, Artifact Registry. Skipped with `create_platform = false` when a cluster already exists | |
| `gke/terraform/modules/air-model-gateway/` | Memorystore, the gateway's secrets, one token secret per calling service, Workload Identity IAM | |
| `gke/terraform/modules/air-service/` | One service's secrets and Workload Identity IAM | |
| `gke/terraform/modules/internal-gateway/` | The orchestrator's front door: the proxy-only subnet, the private static IP, the Cloud Armor rate-limit policy | |
| `gke/scripts/bootstrap-gateway-secrets.sh` | Issues the gateway's service tokens and loads provider keys; rotates tokens | First setup; rotations |
| `gke/scripts/hash-key.sh` | Digest of a raw API key, for a service's key records | When issuing an API key |
| `gke/scripts/release.sh` | The Helm arguments for a render or a deploy, in one place | Never directly: use `make` |
| `gke/scripts/validate.sh`, `smoke-gateway.sh`, `smoke-services.sh` | Static validation and smoke tests | Through `make` |
| `gke/scripts/check-front-door.sh`, `smoke-front-door.sh` | The front door's checks: on the rendered manifests (run by `make gke-validate`), and against the deployed load balancer | Through `make` |
| `gke/kind/` | The laptop rehearsal: cluster definition, a TLS Redis, the up scripts | `make kind-up` |

In each service's own repository:

| What | Where |
| --- | --- |
| The `Dockerfile` and the image build | The service's repository. `make image-push` |
| `make deploy`, `make deploy-smoke`, `make deploy-render` | The service's repository. Thin wrappers that call this repository's `make gke-*` targets, so a service team deploys from its own checkout |
| The gateway's routing, pricing and policy files | `air-model-gateway/deploy/routing.yaml`, `pricing.yaml`, `policy/policy.<env>.yaml`. They are the gateway's configuration and stay with it; the chart receives them at deploy time |
| The build-and-promote pipeline | `air-model-gateway/.github/workflows/deploy.yml` (the only service with one so far) |

The `make` targets, all run from this repository's root:

| Target | Does |
| --- | --- |
| `make gke-validate` | Lints both charts; renders every service for `uat`, `prod` and `kind` and schema-checks every manifest; formats and validates Terraform; runs shellcheck |
| `make gke-render SERVICE=… DEPLOY_ENV=…` | Prints the manifests. `SAMPLE=1` fills the placeholders so you can read them before a project exists |
| `make gke-deploy SERVICE=… DEPLOY_ENV=… KUBE_CONTEXT=… …` | `helm upgrade --install`, atomic, waits for the rollout |
| `make gke-smoke SERVICE=… DEPLOY_ENV=… KUBE_CONTEXT=… …` | Smoke-tests the deployed service |
| `make gke-smoke-front-door FRONT_DOOR_URL=… ORCHESTRATOR_CUSTOMER_KEY=…` | Smoke-tests the orchestrator's front door, from a machine inside the VPC |
| `make kind-up`, `kind-test`, `kind-down` | The laptop rehearsal |

## 3. Prerequisites

- Tools: `gcloud`, `kubectl`, `helm` (v3.14 or later), `terraform` 1.5 or later,
  `kubeconform` for `make gke-validate`, and `kind` for the rehearsal.
- The sibling repositories checked out next to this one. The gateway's is required
  (`GATEWAY_DIR`, default `../air-model-gateway`): its routing, pricing and policy
  files go into its release. The others are needed to build their images.
- A GCS bucket for Terraform state (one bucket; each environment uses its own
  prefix, `air-platform/<env>`). **Restrict it like a secret store**: the Redis AUTH
  string is in the state.
- The region (default `us-central1`) and the project for each environment.
- At least one cloud model provider key (Gemini is first in every cloud chain), and a
  decision on model servers ([02-gcp-deployment.md](02-gcp-deployment.md) §9).

## 4. Rehearse on a laptop first

`kind` runs a real Kubernetes cluster in Docker. The rehearsal deploys all five
services with **the production values**, changing only what kind can't provide
(images are local, secrets are Kubernetes Secrets, no Managed Prometheus). It needs
Docker, `kind`, `kubectl`, `helm`, and Ollama on the host with the two local models
(see air-web-console's local setup guide).

```bash
make gke-validate     # static checks, no cluster needed
make kind-up          # cluster "air-platform", a TLS Redis, all five services
make kind-test        # the gateway's 17 checks and NetworkPolicy; the four services end to end
make kind-down        # delete the cluster (gke/kind/.state keeps the generated keys)
```

`make kind-up` generates its own salts, API keys and gateway tokens into
`gke/kind/.state/` (ignored by git); it does not use the published development keys.
For cloud answers on kind, put a provider key in `air-model-gateway/.env` before
running it.

To look at one service: `kubectl --context kind-air-platform -n air-rag-service get pods`,
and `kubectl --context kind-air-platform -n air-rag-service port-forward service/air-rag-service 8087:80`.

## 5. First-time setup of an environment

Shown for UAT. Repeat with `prod`.

### 5.1 GCP resources

```bash
cd gke/terraform/environments/uat
cp terraform.tfvars.example terraform.tfvars          # project_id, region, create_platform…
terraform init -backend-config="bucket=<state-bucket>"
terraform plan -out plan && terraform apply plan
terraform output                                      # cluster, image_repository, secret ids
```

This creates:

- Optionally (`create_platform = true`) the VPC, Cloud NAT, the Autopilot cluster with
  the Secret Manager add-on, and the Artifact Registry repository `air`.
- Memorystore (BASIC in UAT, STANDARD_HA in prod) with AUTH and TLS, and the gateway's
  Redis URL and CA secrets, filled in by Terraform.
- Empty secret containers for everything else, and `secretAccessor` on each one for
  exactly the Kubernetes service account that needs it (`<service>/<service>`),
  through direct Workload Identity Federation: no Google service account, no key.

### 5.2 The gateway's secrets

```bash
# Provider keys you have, from your password manager; the script pipes them to gcloud on stdin.
export AIR_MODEL_GATEWAY_PROVIDERS__GEMINI_API_KEY=…      # optional
export AIR_MODEL_GATEWAY_PROVIDERS__ANTHROPIC_API_KEY=…   # optional
gke/scripts/bootstrap-gateway-secrets.sh <uat-project> uat
```

This issues one token per calling service into `<service>-model-gateway-token`,
writes the gateway's `token:service` map, and adds provider key versions. Re-runs
keep existing tokens. The script prints no values.

**A secret listed in a chart with no version stops its pods from starting.** If you
have no OpenAI key, remove it from the gateway's list in
`gke/values/uat/air-model-gateway.yaml`:

```yaml
secrets:
  gcp:
    items:
      AIR_MODEL_GATEWAY_PROVIDERS__OPENAI_API_KEY: null
```

### 5.3 The other services' secrets

Each service needs a salt, its API key records, and the keys it uses to call others.
Terraform created the containers; you add the values. Nothing here passes through
Terraform state or a file in git.

The pattern for every value:

```bash
printf %s "<value>" | gcloud secrets versions add <secret-id> --project <uat-project> --data-file=-
```

**Step 1: a salt per service** (any long random string, different per service and
per environment):

```bash
for s in air-classifier-service air-rag-service air-tools-gateway air-orchestrator-service; do
  openssl rand -hex 32 | tr -d '\n' | gcloud secrets versions add $s-hash-salt --project <uat-project> --data-file=-
done
```

**Step 2: API keys.** A key has two halves. The **raw key** is what a caller sends in
`X-API-Key`. The **digest** is what the service stores: `sha256(salt + raw key)`. You
generate a raw key, compute its digest with the service's salt, put the digest in the
service's key records, and give the raw key to the caller.

```bash
salt=$(gcloud secrets versions access latest --secret air-rag-service-hash-salt --project <uat-project>)
raw="airr_uat_$(openssl rand -hex 20)"
gke/scripts/hash-key.sh "$salt" "$raw"        # prints the digest
```

The key records each service expects (one JSON list per service; the full field list
is beside the setting in each repository's `.env.example`):

| Secret id | Content |
| --- | --- |
| `air-classifier-service-api-keys` | `[{"id":"orchestrator","name":"…","key_hash":"<digest>","scopes":["classify:read","admin:read"]}]` |
| `air-rag-service-api-keys` | `[{"id":"orchestrator-<tenant>","name":"…","key_hash":"<digest>","tenant":"<tenant>","scopes":["retrieval:read","admin:read"]}]`. One record per tenant: the tenant comes from the key |
| `air-tools-gateway-api-keys` | `[{"id":"<tenant>-read","tenant":"<tenant>","scope":"read","key_hash":"<digest>"},{"id":"<tenant>-exec","tenant":"<tenant>","scope":"execute","key_hash":"<digest>"}]`. A read key and an execute key per tenant |
| `air-orchestrator-service-api-keys` | `[{"id":"…","name":"…","key_hash":"<digest>","channel":"customer","tenant":"<tenant>","scopes":["chat:write","session:read"],"allow_actions":true}, …]`. One record per client application; `channel` is `customer` or `business`; set `allow_actions` deliberately |

**Step 3: the keys the orchestrator sends.** These are the **raw** halves of the keys
you just issued for the three services it calls:

| Secret id | Content |
| --- | --- |
| `air-orchestrator-service-classifier-api-key` | The raw classifier key |
| `air-orchestrator-service-rag-api-key` | The raw RAG key (one tenant). For several tenants, use the `…__DOWNSTREAM__RAG__TENANT_KEYS` setting instead, and change the secret map in Terraform and the values file to match |
| `air-orchestrator-service-tools-tenant-keys` | `{"<tenant>":{"read":"<raw read key>","execute":"<raw execute key>"}}` |

**Step 4: Redis for the classifier and the tools gateway.** Both can share the
gateway's Memorystore instance (their keys are prefixed `air:clf:` and `airtg`), or
have their own.

| Secret id | Content |
| --- | --- |
| `air-classifier-service-redis-url` | `rediss://:<auth>@<host>:<port>/0?ssl_cert_reqs=required&ssl_ca_certs=/etc/air/redis-ca/ca.pem&ssl_check_hostname=false` |
| `air-tools-gateway-redis-url` | `rediss://:<auth>@<host>:<port>/0` |

Both also need Memorystore's CA mounted at `/etc/air/redis-ca/ca.pem`
(`extraVolumes` in their values files; `gke/values/kind/` shows the exact block), and
the tools gateway needs `NODE_EXTRA_CA_CERTS=/etc/air/redis-ca/ca.pem`. **This path is
proven on kind against a TLS Redis whose certificate matches its host name.
Memorystore is dialled by IP address**, so its certificate will not match: the
classifier's URL above switches host-name checking off for that reason, as the
gateway's does; **the tools gateway has no such switch today**, and may refuse the
connection. Test it in UAT first. If it fails, the fallback is
`AIR_TOOLS_GATEWAY__STORE__BACKEND: memory` with one replica, until the tools gateway
gains the option ([02-gcp-deployment.md](02-gcp-deployment.md) §9).

### 5.4 Values

In each `gke/values/uat/<service>.yaml`:

- `<region>` and `<project>` are placeholders. Either replace them, or leave them and
  pass `IMAGE_REPOSITORY` and `GCP_PROJECT` on every deploy (CI does). The charts
  refuse to render while a placeholder remains.
- **air-model-gateway:** set `modelServers.*` to the model servers' in-cluster URLs.
- **air-rag-service:** uncomment the `extraVolumes` block and
  `AIR_RAG_SERVICE__INGESTION__SEED_DIR`, and mount the knowledge base. Without it the
  service starts with an empty index.
- **air-classifier-service:** mount or bake in the Tier 1 model (the comment in the
  file shows both).
- **air-orchestrator-service:** set the brand name and the customer-token public keys
  (commented in the file).

### 5.5 Who may call whom

Each service's `networkPolicy.allowedNamespaces` already lists its callers:

| Service | Admits |
| --- | --- |
| air-model-gateway | air-classifier-service, air-orchestrator-service, air-rag-service (and the names of two callers that aren't deployed: air-web-console, air-tools) |
| air-classifier-service, air-rag-service, air-tools-gateway | air-orchestrator-service |
| air-orchestrator-service | No namespace. Only its front door, by IP range: the proxy-only subnet and Google's health-check ranges (`networkPolicy.allowedCidrs`, next section) |

### 5.6 The orchestrator's front door

**The decision:** only the shop's backend calls air-orchestrator-service, over a
private address. Browsers and mobile apps never do; they talk to the backend, which
signs a customer token and calls the orchestrator. So the front door is a GKE
Gateway of class `gke-l7-rilb`: a **regional internal Application Load Balancer**
with a private IP in the VPC, HTTPS, and a Cloud Armor rate limit. The requirements
are in the orchestrator's spec, `air-orchestrator-service/docs/specs/0016-front-door/`
(`REQ-FDR-…`).

```
 shop backend (same VPC and region, or a connected network)
      │ HTTPS to <front-door-hostname> → private IP
      ▼
 Gateway (gke-l7-rilb) ── Cloud Armor: 300 requests / 60 s per client IP, then 429
      │ HTTPRoute: only POST /v1/chat, /v1/query, /v1/sessions/…  (everything else 404)
      ▼
 service/air-orchestrator-service:80  (NetworkPolicy admits the proxy-only subnet
                                       and the health-check ranges, nothing else)
```

What is built, and who owns what:

| Piece | Where | Owner |
| --- | --- | --- |
| Gateway API on the cluster | `gke-platform` module (`gateway_api_config`), or `gcloud container clusters update <cluster> --gateway-api=standard` for a cluster Terraform doesn't manage | This repository |
| Proxy-only subnet (`10.129.0.0/23`), private static IP, Cloud Armor policy | `internal-gateway` module, `module "front_door"` in each environment | This repository |
| Gateway, HTTPRoute, health check, backend policy (timeout, draining, Cloud Armor, logging) | `gke/helm/air-service/templates/gateway.yaml`, switched on by `gateway.enabled` in the orchestrator's values | This repository |
| The DNS name and the TLS certificate | Your private DNS zone and your certificate authority | **The platform team.** Not created here |

**First-time setup** (UAT shown; prod is the same with `prod`):

1. **Terraform.** If the VPC already has a proxy-only subnet in this region (one is
   allowed per region and VPC, and any other internal load balancer there already
   made one), set `create_proxy_only_subnet = false` and `proxy_only_cidr` to its
   range in `terraform.tfvars`. If the cluster isn't Terraform's
   (`create_platform = false`), also set `subnetwork_id`. Then apply, and read:

   ```bash
   terraform output front_door
   # address          the private IP
   # address_name     -> gateway.addressName
   # security_policy  -> gateway.securityPolicy
   # proxy_only_cidr  -> must be in networkPolicy.allowedCidrs
   ```

2. **DNS.** In a private Cloud DNS zone visible to the backend's network, add an A
   record from the hostname you choose to `address`.

3. **Certificate.** Get a certificate for that hostname from your CA. Either put it
   in a Kubernetes Secret in the orchestrator's namespace:

   ```bash
   kubectl -n air-orchestrator-service create secret tls air-front-door-tls \
     --cert=fullchain.pem --key=privkey.pem
   ```

   and set `gateway.tls.secretName`; or upload it as a regional SSL certificate
   (`gcloud compute ssl-certificates create … --region <region>`) and set
   `gateway.tls.preSharedCerts` instead. Exactly one of the two. Google-managed
   certificates are not available for an internal regional load balancer; a
   Certificate Manager certificate is, if you prefer it (not wired in the chart).
   The backend must trust the CA that signed it.

4. **Values.** In `gke/values/uat/air-orchestrator-service.yaml`, replace
   `<front-door-hostname>` and `<front-door-tls-secret>`. Check `addressName`,
   `securityPolicy` and `allowedCidrs` against step 1's output. The chart refuses to
   render while a placeholder remains, while `publicPaths` is empty, or with both or
   neither TLS option.

5. **Deploy** the orchestrator as usual (section 6). The load balancer takes a few
   minutes to program. Watch it:

   ```bash
   kubectl -n air-orchestrator-service get gateway,httproute
   kubectl -n air-orchestrator-service describe gateway air-orchestrator-service   # Programmed=True
   ```

6. **Smoke-test from inside the network** (a VM in the VPC, or through the VPN; the
   address is not reachable from anywhere else):

   ```bash
   make gke-smoke-front-door FRONT_DOOR_URL=https://<front-door-hostname> \
     ORCHESTRATOR_CUSTOMER_KEY=<raw customer key> [CA_FILE=ca.pem] [RATE_BURST=400]
   ```

   It checks that a chat turn answers, `X-Request-ID` comes back, TLS holds, the
   internal routes (`/v1/health`, `/v1/ready`, `/v1/capabilities`, `/metrics`,
   `/docs`) are a 404, a streamed answer arrives in pieces rather than all at the
   end, and, with `RATE_BURST`, that the limit answers 429. The burst spends that
   minute's allowance for the machine you run it from.

Rules that keep it working:

- **Only the public routes.** The route forwards `POST /v1/chat`, `POST /v1/query`
  and `/v1/sessions/…`. Health, readiness, capabilities, metrics and the docs stay
  inside the cluster. When the orchestrator gains a public route, add it to the
  spec's public set (its test fails until you do) and to `gateway.publicPaths`.
- **Streaming is not buffered.** The load balancer passes Server-Sent Events
  through; the service sets `X-Accel-Buffering: no` and `Cache-Control: no-cache`.
  Don't put a buffering proxy in front of it.
- **Timeout.** `gateway.timeoutSec` (60 s) is the whole response, stream included.
  Keep it at least four times the turn deadline (15 s); `make gke-validate` checks.
- **Rate limit.** 300 requests per 60 s per client IP, then 429
  (`front_door_rate_limit_count` in Terraform). With one backend calling, that is
  one budget for **all** its customers: size it to the backend's peak, or key it on
  a header the backend sets (the `internal-gateway` module's
  `rate_limit_enforce_on_key = "HTTP_HEADER"` and `rate_limit_header_name`, passed
  in `module "front_door"`). The orchestrator's
  own per-key limit is configured but not enforced (air-web-console bug OR-017), so
  this is the only limit.
- **Identity comes from the key, never from proxy headers.** `X-Forwarded-For` and
  the like are ignored by the service (spec 0016, tested). Callers still need their
  orchestrator API key and, for a customer, a signed customer token.
- **Who can reach the address.** Clients in the same VPC and region, or a network
  connected to it by VPN, Interconnect or peering. To call it from another region,
  turn on global access (not configured).
- **Logs.** The load balancer logs requests (sample rate 100%); it does not log
  request headers, so `X-API-Key` doesn't reach the logs. Keep it that way.

On kind, `gateway.enabled` is `false`: an internal load balancer needs GCP, so the
front door is checked on its rendered manifests only until UAT.

## 6. Deploying a service

Deploy in dependency order the first time: **air-model-gateway**, then
**air-classifier-service**, **air-rag-service** and **air-tools-gateway** in any
order, then **air-orchestrator-service**.

From the service's own repository (what a service team does):

```bash
gcloud auth configure-docker <region>-docker.pkg.dev
gcloud container clusters get-credentials <cluster> --region <region> --project <project>

cd ../air-rag-service
export IMAGE_REPOSITORY=<region>-docker.pkg.dev/<project>/air/air-rag-service
make image-push                                  # builds linux/amd64, tags with the commit, pushes
make deploy DEPLOY_ENV=uat GCP_PROJECT=<project> KUBE_CONTEXT=$(kubectl config current-context)
make deploy-smoke DEPLOY_ENV=uat KUBE_CONTEXT=$(kubectl config current-context)
```

Or the same from this repository (what DevOps does, for an image that already exists):

```bash
make gke-deploy SERVICE=air-rag-service DEPLOY_ENV=uat KUBE_CONTEXT=<ctx> GCP_PROJECT=<project> \
  IMAGE_REPOSITORY=<region>-docker.pkg.dev/<project>/air/air-rag-service IMAGE_DIGEST=sha256:…
make gke-smoke SERVICE=air-rag-service DEPLOY_ENV=uat KUBE_CONTEXT=<ctx>
```

- `KUBE_CONTEXT` is never defaulted, so a deploy can't land on whatever cluster
  `kubectl` happens to point at.
- **Prefer `IMAGE_DIGEST` to `IMAGE_TAG`** for anything that matters: a digest can't
  be re-pointed after review. `make image-push` tags with the commit; the digest is in
  `docker push`'s output and in Artifact Registry.
- A deploy is atomic: if the pods don't become ready in 10 minutes, Helm rolls back.
- **Production is a promotion, not a rebuild.** Copy the digest UAT tested into
  prod's registry and deploy that digest:
  `docker buildx imagetools create --tag <prod-repository>:<tag> <uat-repository>@sha256:…`

What the smoke tests check:

| Service | `make gke-smoke` checks |
| --- | --- |
| air-model-gateway | Two ready replicas; then, from a pod in a temporary calling namespace: readiness, Redis, a development token refused, each caller's grants (200 or 403), a stream, the environment name; and that a pod in an unlisted namespace can't connect. Needs `GCP_PROJECT` to read the callers' tokens |
| The other four | The rollout; `helm test` (health and readiness from inside the cluster, through the NetworkPolicy); what `/v1/ready` reports. With `KEYS_FILE=<env file of raw keys>`, one authenticated call each, and for the orchestrator a whole conversation turn |

The variables `KEYS_FILE` may hold: `CLASSIFIER_KEY`, `RAG_KEY`, `TOOLS_READ_KEY`,
`ORCHESTRATOR_CUSTOMER_KEY`. Keep that file out of git.

### Pipelines

Only air-model-gateway has a deploy pipeline
(`air-model-gateway/.github/workflows/deploy.yml`): on a `v*` tag or by hand, it
builds once, deploys UAT by digest, smoke-tests, waits for approval on the `prod`
environment, copies the same digest to prod, deploys and smoke-tests. It checks this
repository out beside the gateway's and calls `make deploy`. It is the template for
the other four; they have none yet.

Set up once for a pipeline:

- A Workload Identity Federation pool and provider trusting the GitHub repository.
- A deploy service account per environment with `roles/artifactregistry.writer`,
  `roles/container.developer`, and, for the gateway's smoke test,
  `roles/secretmanager.secretAccessor` on the `*-model-gateway-token` secrets; on
  prod's account also `roles/artifactregistry.reader` on UAT's repository.
- GitHub environments `uat` and `prod` with variables `GCP_PROJECT_ID`, `GCP_REGION`,
  `GKE_CLUSTER`, `GCP_WIF_PROVIDER` and `GCP_DEPLOY_SA`. Prod also needs
  `UAT_IMAGE_REPOSITORY`, and a required reviewer.
- If this repository is private, a token that lets the pipeline read it
  (`INFRA_REPO_TOKEN`).
- With a private control plane, the runners must be allowed through
  `master_authorized_networks`, or be self-hosted inside the VPC.

This repository's own CI (`.github/workflows/ci.yml`) runs `make check` and
`make gke-validate` on every push.

## 7. Day-2 operations

| Task | How |
| --- | --- |
| Change a non-secret setting | Edit `env:` in `gke/values/<env>/<service>.yaml`, deploy. The pods roll |
| Change a secret | `gcloud secrets versions add …`, then `kubectl -n <service> rollout restart deployment/<service>`. Secrets are read at start |
| Change the gateway's routing, pricing or a grant | Edit the file in `air-model-gateway/deploy/`, merge, deploy the gateway (same image). The ConfigMap checksum rolls the pods |
| Rotate a provider key | Add a secret version, restart the gateway |
| Rotate the gateway's service tokens, no downtime | `bootstrap-gateway-secrets.sh <project> <env> --rotate` (the gateway accepts old and new), restart the gateway, restart each caller, then `--retire-old` and restart the gateway |
| Rotate an API key of another service | Add the new digest **beside** the old one in the service's key records and restart it; switch the caller's raw key and restart the caller; then remove the old digest and restart the service |
| Roll back | `helm rollback <service> -n <service>` (settings and image together) |
| Scale | HPA on CPU for the gateway, the classifier and the tools gateway. **Not** the orchestrator or the RAG service (section 9 of [02-gcp-deployment.md](02-gcp-deployment.md)) |
| Deploy the orchestrator | Expect every conversation in progress to end: its strategy is `Recreate` and its sessions are in memory. Deploy it outside busy hours |
| Update the knowledge base | Replace the mounted documents and restart air-rag-service: the index is built at start |
| Redis outage | The gateway and the classifier degrade and keep serving (per-replica state meanwhile). The tools gateway turns unready: proposals and idempotency records live there |
| A model provider outage | The gateway's breakers skip the broken model and chains fail over. If every provider fails, its readiness turns 503 |

### Logs

Every service writes one JSON object per line to stdout. In Cloud Logging:

```
resource.type="k8s_container"
resource.labels.namespace_name="air-orchestrator-service"
jsonPayload.request_id="req_…"
```

`request_id` is the thread to pull: every service echoes `X-Request-ID` and forwards
it, so the same id appears in the orchestrator's, the classifier's, the RAG service's
and the gateway's logs for one conversation turn. The gateway writes one audit line
per model call (`jsonPayload.event="model.call"`); the tools gateway writes one per
executed change (`action.executed`).

### Metrics and alerts

Managed Prometheus scrapes `/metrics` on every pod (`PodMonitoring`). Each
repository documents its own metrics. Alerts worth creating first:

- **Any service:** `kube_deployment_status_replicas_available` below what it should
  be; a 5xx ratio per route; p95 latency above its target.
- **air-model-gateway** (all prefixed `air_model_gateway_`): `circuit_open == 1` for
  5 minutes (a model is down); any `budget_rejected_total` or
  `provider_quota_rejected_total` (a budget or quota is being hit);
  `rung_failures_total{reason="auth"}` rising (a provider refused the key or the
  account is out of credit: failover hides it from callers, usually at a higher
  price); `model_unpriced_total` rising (uncounted spend).
- **air-classifier-service** (`air_classifier_`): the share of `decided_by_total` on
  `t3_cloud_llm` (cost), `degraded_responses_total`, `breaker_state`.
- **air-orchestrator-service** (`air_orchestrator_service_`): refused and degraded
  turns, and `customer_tokens_total{outcome}` for rejected customer tokens.
- **air-tools-gateway** (`air_tools_gateway_`): `requests_total` by status on the
  execute route.

No dashboards exist yet.

## 8. Security posture

- **Identity between services:** every call carries an API key; the receiving service
  checks it against salted digests. The gateway additionally checks each caller's
  grants in its policy file, which fails closed outside local development. The
  NetworkPolicy is a second, network-level gate.
- **Secrets** live only in Secret Manager. Pods read them through Workload Identity,
  each service account limited to its own secrets. Nothing is in a Kubernetes
  Secret, GitHub, a ConfigMap or an image.
  - The gateway reads its secrets as files, natively.
  - The other four read environment variables. Their chart mounts each secret as a
    file named like the variable and starts the container through a three-line
    shell wrapper that exports the files and then `exec`s the service
    (`gke/helm/air-service/templates/deployment.yaml`). The values therefore exist
    in the process environment, as they would with any `env:`-based setup, and not
    in the pod spec.
- **Every pod:**
  - runs as a non-root user with a read-only root filesystem (proven on kind for all
    five);
  - drops all capabilities, with no privilege escalation;
  - uses the `RuntimeDefault` seccomp profile;
  - has no Kubernetes API token mounted.
- **Redis:** AUTH plus TLS, verified against the instance's CA.
- **Supply chain:** registry tags are immutable; deploy by digest; production runs
  exactly what UAT tested.
- **In-cluster traffic is plain HTTP.** If your policy requires encryption between
  pods, enable GKE inter-node transparent encryption or a service mesh. No service
  needs a change for either.
- **Egress isn't restricted by the charts.** Only the gateway needs the internet. To
  pin it, use GKE FQDN network policies (`generativelanguage.googleapis.com`,
  `api.anthropic.com`, `api.openai.com`) or a Cloud NAT firewall policy.

## 9. What has been verified, and what hasn't

Verified on a laptop on 2026-11-17 (`make gke-validate`, `make kind-up`,
`make kind-test`):

- **Charts:** both lint. Every service's manifests for `uat`, `prod` and `kind` pass
  Kubernetes 1.33 and CRD schema validation (`SecretProviderClass`, `PodMonitoring`).
- **Terraform:** both roots pass `fmt` and `validate` (Terraform 1.5.7).
- **The orchestrator's front door, on its rendered manifests** (`check-front-door.sh`,
  for `uat` and `prod`): the route forwards exactly the three public paths, the
  health check is on `/v1/health`, the timeout is at least four turn deadlines, the
  class is internal with an HTTPS listener, the NetworkPolicy admits the load
  balancer's ranges, and the Cloud Armor policy is attached. The service's side
  (proxy headers ignored, no CORS, every route public or internal) is tested in the
  orchestrator's own `make check`. `smoke-front-door.sh` was tried against the
  service directly (a port-forward), not against a load balancer.
- **On kind, with the production values of all five services:**
  - every pod starts with a read-only root filesystem and its real start command;
  - secrets reach every service (files for the gateway, the wrapper for the others);
  - the gateway passes its 17 smoke checks, and its NetworkPolicy blocks an unlisted
    namespace;
  - the classifier (2 replicas) and the tools gateway (2 replicas) use Redis over TLS
    with AUTH and CA verification;
  - the RAG service indexes its sample documents through the gateway (29 chunks) and
    returns citations;
  - the orchestrator reports all three optional services configured, and a real
    conversation turn through it comes back `ok`, not degraded, with a cited answer;
  - each NetworkPolicy admits its listed callers: the calls above cross four
    namespaces.

Carried over from the gateway's own earlier verification, not repeated here: a
rolling restart under continuous traffic dropped 0 of 1,331 requests; and
`bootstrap-gateway-secrets.sh` first run, re-run, `--rotate` and `--retire-old`
against a stand-in for `gcloud` (the script has since changed only in the
environment names it accepts).

Not verifiable without a GCP project:

- `terraform apply`;
- the Secret Manager add-on mounting real secrets, for either chart;
- Memorystore's real CA, and in particular the tools gateway against a certificate
  that doesn't match the host it dials (section 5.3);
- Managed Prometheus scraping;
- Autopilot admission of the pod specs;
- the deploy pipeline's Workload Identity Federation;
- the front door itself: the load balancer programming, TLS, the rate limit's 429,
  and unbuffered streaming through it (`make gke-smoke-front-door` in UAT,
  section 5.6).

Not built:

- The front door's DNS record and certificate (the platform team's, section 5.6).
- Deploy pipelines for four of the five services.
- Model servers in the cluster.
- Dashboards.
