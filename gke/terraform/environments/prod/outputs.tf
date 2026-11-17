output "cluster" {
  description = "gcloud container clusters get-credentials <name> --region <location>"
  value = var.create_platform ? {
    name     = module.platform[0].cluster_name
    location = module.platform[0].cluster_location
  } : null
}

output "image_repository" {
  value = var.create_platform ? module.platform[0].image_repository : null
}

output "redis_host" {
  value = module.gateway.redis_host
}

output "gateway_secret_ids" {
  value = module.gateway.gateway_secret_ids
}

output "consumer_token_secret_ids" {
  value = module.gateway.consumer_token_secret_ids
}

output "workload_principal" {
  value = module.gateway.workload_principal
}

output "service_secret_ids" {
  description = "Per service: setting name -> Secret Manager id (the chart's secrets.gcp.items)."
  value = {
    air-classifier-service   = module.classifier.secret_ids
    air-rag-service          = module.rag.secret_ids
    air-tools-gateway        = module.tools.secret_ids
    air-orchestrator-service = module.orchestrator.secret_ids
  }
}

output "service_workload_principals" {
  value = {
    air-classifier-service   = module.classifier.workload_principal
    air-rag-service          = module.rag.workload_principal
    air-tools-gateway        = module.tools.workload_principal
    air-orchestrator-service = module.orchestrator.workload_principal
  }
}
