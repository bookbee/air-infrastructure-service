# The AIR platform, prod: the shared GKE platform (optional), air-model-gateway's
# Redis and secrets, and the secrets and identity of the four services deployed
# with the shared chart. Apply order and what to do after are in
# docs/03-gke-runbook.md.

locals {
  labels = { platform = "air-platform", environment = "prod" }
}

module "platform" {
  source = "../../modules/gke-platform"
  count  = var.create_platform ? 1 : 0

  project_id                 = var.project_id
  region                     = var.region
  master_authorized_networks = var.master_authorized_networks
  deletion_protection        = true
  labels                     = local.labels
}

module "gateway" {
  source = "../../modules/air-model-gateway"

  project_id  = var.project_id
  region      = var.region
  environment = "prod"
  network_id  = var.create_platform ? module.platform[0].network_id : var.network_id
  redis_tier  = "STANDARD_HA"
  labels      = local.labels
}

# ---- the four services on the shared chart -----------------------------------
# Each map is setting name -> secret id, and must match secrets.gcp.items in
# gke/values/prod/<service>.yaml. The gateway token of a service that calls
# air-model-gateway is not listed here: its secret belongs to the gateway's
# module, and gateway_token_secret_id grants read access to it.

module "classifier" {
  source = "../../modules/air-service"

  project_id  = var.project_id
  environment = "prod"
  service     = "air-classifier-service"
  labels      = local.labels
  secrets = {
    AIR_CLASSIFIER__SECURITY__HASH_SALT       = "air-classifier-service-hash-salt"
    AIR_CLASSIFIER__SECURITY__API_KEYS_INLINE = "air-classifier-service-api-keys"
    AIR_CLASSIFIER__CACHE__REDIS_URL          = "air-classifier-service-redis-url"
  }
  gateway_token_secret_id = module.gateway.consumer_token_secret_ids["air-classifier-service"]
}

module "rag" {
  source = "../../modules/air-service"

  project_id  = var.project_id
  environment = "prod"
  service     = "air-rag-service"
  labels      = local.labels
  secrets = {
    AIR_RAG_SERVICE__SECURITY__HASH_SALT       = "air-rag-service-hash-salt"
    AIR_RAG_SERVICE__SECURITY__API_KEYS_INLINE = "air-rag-service-api-keys"
  }
  gateway_token_secret_id = module.gateway.consumer_token_secret_ids["air-rag-service"]
}

# No gateway token: air-tools-gateway makes no model call, by rule.
module "tools" {
  source = "../../modules/air-service"

  project_id  = var.project_id
  environment = "prod"
  service     = "air-tools-gateway"
  labels      = local.labels
  secrets = {
    AIR_TOOLS_GATEWAY__SECURITY__HASH_SALT = "air-tools-gateway-hash-salt"
    AIR_TOOLS_GATEWAY__SECURITY__API_KEYS  = "air-tools-gateway-api-keys"
    AIR_TOOLS_GATEWAY__STORE__REDIS_URL    = "air-tools-gateway-redis-url"
  }
}

module "orchestrator" {
  source = "../../modules/air-service"

  project_id  = var.project_id
  environment = "prod"
  service     = "air-orchestrator-service"
  labels      = local.labels
  secrets = {
    AIR_ORCHESTRATOR_SERVICE__SECURITY__HASH_SALT                    = "air-orchestrator-service-hash-salt"
    AIR_ORCHESTRATOR_SERVICE__SECURITY__API_KEYS_INLINE              = "air-orchestrator-service-api-keys"
    AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__CLASSIFIER__API_KEY        = "air-orchestrator-service-classifier-api-key"
    AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__RAG__API_KEY               = "air-orchestrator-service-rag-api-key"
    AIR_ORCHESTRATOR_SERVICE__DOWNSTREAM__TOOLS_GATEWAY__TENANT_KEYS = "air-orchestrator-service-tools-tenant-keys"
  }
  gateway_token_secret_id = module.gateway.consumer_token_secret_ids["air-orchestrator-service"]
}
