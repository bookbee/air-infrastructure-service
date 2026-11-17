# gke/: deploying the AIR services to Google Cloud

Everything DevOps needs to deploy the five core services (air-model-gateway,
air-classifier-service, air-rag-service, air-tools-gateway,
air-orchestrator-service) to **UAT** and **production** on GKE, in one place.

- **The procedure:** [`docs/03-gke-runbook.md`](../docs/03-gke-runbook.md)
- **The reasoning** (what each service needs, what replaces the local stack):
  [`docs/02-gcp-deployment.md`](../docs/02-gcp-deployment.md)

The rest of this repository (the Go service, `docker-compose.yml`, `deploy/`) is the
local development stack and is never deployed. This folder is the opposite: it is
only about the cloud.

```text
gke/
├── helm/
│   ├── air-model-gateway/     the gateway's own chart (it mounts routing, pricing and policy files)
│   └── air-service/           one shared chart for the other four services
├── values/
│   ├── uat/<service>.yaml     one file per service per environment
│   ├── prod/<service>.yaml
│   └── kind/<service>.yaml    the laptop rehearsal: overrides on top of prod
├── terraform/
│   ├── environments/uat, prod the two roots you `terraform apply`
│   └── modules/
│       ├── gke-platform/      VPC, NAT, Autopilot cluster, Artifact Registry (optional)
│       ├── air-model-gateway/ Memorystore, the gateway's secrets and tokens, IAM
│       └── air-service/       one service's secrets and IAM
├── scripts/
│   ├── release.sh             the Helm arguments for a render or a deploy, in one place
│   ├── validate.sh            make gke-validate
│   ├── bootstrap-gateway-secrets.sh   issue and rotate the gateway's tokens; load provider keys
│   ├── hash-key.sh            digest of a raw API key
│   ├── smoke-gateway.sh, smoke_gateway.py   make gke-smoke SERVICE=air-model-gateway
│   └── smoke-services.sh      make gke-smoke for the other four
└── kind/                      make kind-up: a local Kubernetes cluster with all five services
```

## The commands

Run from the repository root. `make help` lists them.

```bash
make gke-validate                                      # static checks; no cluster, no project
make gke-render SERVICE=air-rag-service DEPLOY_ENV=uat SAMPLE=1   # read the manifests
make kind-up && make kind-test                         # rehearse on a laptop
make gke-deploy SERVICE=air-rag-service DEPLOY_ENV=uat KUBE_CONTEXT=… GCP_PROJECT=… \
     IMAGE_REPOSITORY=… IMAGE_DIGEST=sha256:…          # deploy
make gke-smoke  SERVICE=air-rag-service DEPLOY_ENV=uat KUBE_CONTEXT=…
```

Each service's own repository has `make image-push`, `make deploy` and
`make deploy-smoke`, which build the image there and then call the targets above.

## Who owns what

| Thing | Lives in | Changed by |
| --- | --- | --- |
| Charts, values, Terraform, deploy and smoke scripts | here | DevOps |
| A service's `Dockerfile`, image and start command | the service's repository | The service's team. **If a start command, port or user changes, update `command`, `containerPort` or `runAsUser` in that service's values files here** |
| A service's settings (the environment variables it understands) | the service's `.env.example` | The service's team. The values set per environment are here |
| The gateway's routing, pricing and policy | `air-model-gateway/deploy/` | The gateway's team. Passed into its release at deploy time |
| Secret values | Secret Manager | DevOps. Never in git, a values file or Terraform state (the Redis URL excepted: Terraform writes it) |

## Adding a sixth service

1. Its values files: copy `values/uat/air-tools-gateway.yaml` and the `prod` and
   `kind` ones; set the image, `command`, `containerPort`, `env` and `secrets`.
2. Its Terraform: one `module "…" { source = "../../modules/air-service" … }` block in
   each environment's `main.tf`, with the same secret map as the values file.
3. Add its name to the service lists in `scripts/release.sh`, `scripts/validate.sh`
   and, for the rehearsal, `kind/up-services.sh`.
4. Its callers' namespaces in its `networkPolicy.allowedNamespaces`, and its namespace
   in the `allowedNamespaces` of whatever it calls.
5. `make gke-validate`.
