output "workload_principal" {
  description = "IAM principal of the service's Kubernetes service account."
  value       = local.workload_principal
}

output "secret_ids" {
  description = "Setting name -> Secret Manager id. The same map goes in the chart's secrets.gcp.items."
  value       = { for k, s in google_secret_manager_secret.this : k => s.secret_id }
}
