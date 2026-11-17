BINARY   := air-infrastructure-service
PKG      := ./cmd/$(BINARY)
BIN      := bin/$(BINARY)
COMPOSE  ?= docker compose

HOST     ?= 0.0.0.0
PORT     ?= 8080

# The network every AIR service joins. Declared in docker-compose.yml; siblings
# reference it as external, so it must exist before they come up.
NETWORK  := air-net

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "$(BINARY) — make <target>"
	@echo
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Overridable: HOST=$(HOST) PORT=$(PORT)"
	@echo "GKE targets: SERVICE=<one of the five core services> DEPLOY_ENV=uat|prod (docs/03-gke-runbook.md)"

.PHONY: env
env: ## Create .env from .env.example if it does not exist
	@if [ ! -f .env ]; then \
		cp .env.example .env && echo "wrote .env from .env.example"; \
	elif ! grep -q '^AIR_INFRASTRUCTURE_SERVICE__' .env; then \
		echo "WARNING: .env has no AIR_INFRASTRUCTURE_SERVICE__* keys."; \
		echo "  It predates the settings rename, so this service will read none of it —"; \
		echo "  the symptom is a 401 on every call because no service tokens load."; \
		echo "  Fix: back up any local edits, then  rm .env && make env"; \
		exit 1; \
	else \
		echo ".env already exists, leaving it alone"; \
	fi

# ---- local stack -----------------------------------------------------------
.PHONY: up
up: ## Start the shared stack (redis, postgres, pgbouncer, mongo, and this service)
	$(COMPOSE) up -d --build
	@echo
	@echo "service:  http://localhost:$(PORT)/v1/health  (compose service 'air-infrastructure-service')"
	@echo "network:  $(NETWORK) — sibling services join this"

.PHONY: down
down: ## Stop the stack (keep volumes). Removes every profile's containers.
	$(COMPOSE) --profile tools --profile observability down

.PHONY: nuke
nuke: ## Stop the stack and DELETE all volumes (data loss)
	$(COMPOSE) --profile tools --profile observability down -v

.PHONY: up-tools
up-tools: ## Also start dev UIs (adminer :8090, mongo-express :8091, redisinsight :8092)
	$(COMPOSE) --profile tools up -d

.PHONY: up-observability
up-observability: ## Also start prometheus :9090 + grafana :3000 (scrapes every AIR service)
	$(COMPOSE) --profile observability up -d
	@echo "prometheus: http://localhost:9090/targets · grafana: http://localhost:3000"

.PHONY: ps
ps: ## Show stack status
	$(COMPOSE) --profile tools --profile observability ps

.PHONY: logs
logs: ## Tail this service's logs (SVC=<name> to pick another)
	$(COMPOSE) logs -f $(or $(SVC),air-infrastructure-service)

.PHONY: seed
seed: ## Re-run store bootstrap (idempotent init scripts)
	$(COMPOSE) exec postgres psql -U air -d air -f /docker-entrypoint-initdb.d/init.sql

.PHONY: doctor
doctor: ## Verify the shared stack is serving what sibling services expect
	@NETWORK=$(NETWORK) PORT=$(PORT) COMPOSE="$(COMPOSE)" bash scripts/doctor.sh

# ---- dev inner loop --------------------------------------------------------
.PHONY: build
build: ## Build the binary into bin/
	go build -trimpath -o $(BIN) $(PKG)

.PHONY: run
run: ## Run on the host against the stack's published ports
	@# .env addresses the stores by compose service name, which does not resolve
	@# from the host. Override with the published-port equivalents so the native
	@# inner loop talks to the same containers the container build does.
	AIR_INFRASTRUCTURE_SERVICE__REDIS__URL=redis://:localdevpass@localhost:6379/0 \
	AIR_INFRASTRUCTURE_SERVICE__POSTGRES__HOST=localhost \
	AIR_INFRASTRUCTURE_SERVICE__MONGO__URI=mongodb://air:localdevpass@localhost:27017 \
	AIR_INFRASTRUCTURE_SERVICE__API__HOST=$(HOST) \
	AIR_INFRASTRUCTURE_SERVICE__API__PORT=$(PORT) \
	go run $(PKG)

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: cover
cover: ## Run tests with a coverage summary
	@# -coverpkg=./... so a package exercised only by another package's tests
	@# (the handlers, driven from internal/app) counts as covered rather than
	@# reading as a bare 0%.
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: fmt
fmt: ## Format the tree
	gofmt -w .

.PHONY: vet
vet: ## go vet
	go vet ./...

# Tools installed with `go install` land in GOPATH/bin, which is not always on
# PATH. Looking there too means `make lint` finds them without every developer
# having to fix their shell first.
GOBIN := $(shell go env GOPATH)/bin
GOLANGCI := $(shell command -v golangci-lint 2>/dev/null || echo $(GOBIN)/golangci-lint)
STATICCHECK := $(shell command -v staticcheck 2>/dev/null || echo $(GOBIN)/staticcheck)

.PHONY: lint
lint: ## Lint (golangci-lint if installed, else gofmt + staticcheck)
	@test -z "$$(gofmt -l . | tee /dev/stderr)" || { echo "gofmt: the files above need formatting — run 'make fmt'"; exit 1; }
	@if test -x "$(GOLANGCI)"; then \
		"$(GOLANGCI)" run; \
	elif test -x "$(STATICCHECK)"; then \
		echo "golangci-lint not installed — using gofmt + staticcheck"; \
		"$(STATICCHECK)" ./...; \
	else \
		echo "neither golangci-lint nor staticcheck installed — gofmt and 'make vet' only."; \
		echo "  go install honnef.co/go/tools/cmd/staticcheck@latest"; \
	fi

.PHONY: tools
tools: ## Install the optional lint toolchain into GOPATH/bin
	go install honnef.co/go/tools/cmd/staticcheck@latest

.PHONY: check
check: lint vet test ## Lint + vet + test  ← run before calling work done

.PHONY: clean
clean: ## Remove build output and coverage artifacts
	rm -rf bin coverage.out

# ---- GKE: UAT and production for the five core services ---------------------
# Everything DevOps deploys is under gke/: two Helm charts (air-model-gateway's
# own, and the shared air-service chart for the other four), one values file per
# service per environment, Terraform for the GCP resources, and the scripts.
# docs/03-gke-runbook.md is the runbook; gke/README.md is the map.
#
# Each core repository builds and pushes its own image, then calls these
# targets through its own `make deploy DEPLOY_ENV=uat|prod`.
#
#   SERVICE       air-model-gateway | air-classifier-service | air-rag-service |
#                 air-tools-gateway | air-orchestrator-service
#   DEPLOY_ENV    uat | prod
#   KUBE_CONTEXT  required by gke-deploy / gke-smoke; never defaulted, so a
#                 deploy can't land on whatever cluster kubectl points at
#   IMAGE_REPOSITORY, IMAGE_DIGEST (or IMAGE_TAG), GCP_PROJECT
#                 replace the <placeholders> in gke/values/<env>/<service>.yaml
#   GATEWAY_DIR   the air-model-gateway checkout: its routing, pricing and
#                 policy files are passed to the gateway's chart from there
DEPLOY_ENV  ?= uat
GATEWAY_DIR ?= ../air-model-gateway
KIND_CONTEXT := kind-air-platform
GKE_ENVVARS  = GATEWAY_DIR="$(abspath $(GATEWAY_DIR))" KUBE_CONTEXT="$(KUBE_CONTEXT)" \
	IMAGE_REPOSITORY="$(IMAGE_REPOSITORY)" IMAGE_DIGEST="$(IMAGE_DIGEST)" IMAGE_TAG="$(IMAGE_TAG)" \
	GCP_PROJECT="$(GCP_PROJECT)"

.PHONY: gke-require-service
gke-require-service:
	@test -n "$(SERVICE)" || { echo "set SERVICE, e.g. SERVICE=air-rag-service (see 'make help')"; exit 1; }

.PHONY: gke-validate
gke-validate: ## Lint both charts, schema-check every service and environment, validate Terraform, shellcheck
	@GATEWAY_DIR="$(abspath $(GATEWAY_DIR))" gke/scripts/validate.sh

.PHONY: gke-render
gke-render: gke-require-service ## Print the manifests for SERVICE in DEPLOY_ENV (SAMPLE=1 fills the placeholders)
	@$(GKE_ENVVARS) SAMPLE="$(SAMPLE)" gke/scripts/release.sh render $(SERVICE) $(DEPLOY_ENV)

.PHONY: gke-deploy
gke-deploy: gke-require-service ## helm upgrade --install SERVICE for DEPLOY_ENV into KUBE_CONTEXT
	@$(GKE_ENVVARS) gke/scripts/release.sh deploy $(SERVICE) $(DEPLOY_ENV)

.PHONY: gke-smoke
gke-smoke: gke-require-service ## Smoke-test SERVICE in KUBE_CONTEXT (gateway: tokens from GCP_PROJECT; others: KEYS_FILE optional)
	@test -n "$(KUBE_CONTEXT)" || { echo "set KUBE_CONTEXT"; exit 1; }
	@if [ "$(SERVICE)" = air-model-gateway ]; then \
		test -n "$(GCP_PROJECT)$(TOKENS_FILE)" || { echo "set GCP_PROJECT (or TOKENS_FILE)"; exit 1; }; \
		KUBE_CONTEXT="$(KUBE_CONTEXT)" GCP_PROJECT="$(GCP_PROJECT)" TOKENS_FILE="$(TOKENS_FILE)" \
			EXPECT_ENVIRONMENT=$(if $(filter uat,$(DEPLOY_ENV)),staging,prod) gke/scripts/smoke-gateway.sh; \
	else \
		KUBE_CONTEXT="$(KUBE_CONTEXT)" SERVICES="$(SERVICE)" KEYS_FILE="$(KEYS_FILE)" \
			EXPECT_CITATIONS="$(EXPECT_CITATIONS)" gke/scripts/smoke-services.sh; \
	fi

.PHONY: kind-up
kind-up: ## All five services on a local kind cluster, with the production charts and values
	@GATEWAY_DIR="$(abspath $(GATEWAY_DIR))" gke/kind/up-gateway.sh
	@gke/kind/up-services.sh

.PHONY: kind-test
kind-test: ## Smoke-test the kind deployment: the gateway, its NetworkPolicy, and the four services end to end
	@KUBE_CONTEXT=$(KIND_CONTEXT) TOKENS_FILE=gke/kind/.state/tokens.env gke/scripts/smoke-gateway.sh
	@KUBE_CONTEXT=$(KIND_CONTEXT) KEYS_FILE=gke/kind/.state/services.env EXPECT_CITATIONS=true gke/scripts/smoke-services.sh

.PHONY: kind-down
kind-down: ## Delete the kind cluster (keeps gke/kind/.state)
	kind delete cluster --name air-platform
