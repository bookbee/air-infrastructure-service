# What one AIR service owns in one environment: its secrets, and the IAM that
# lets its pods (and only its pods) read them. Used for the four services
# deployed with the shared chart (gke/helm/air-service): air-classifier-service,
# air-rag-service, air-tools-gateway and air-orchestrator-service.
# air-model-gateway has its own module, because it also owns Memorystore.

data "google_project" "this" {
  project_id = var.project_id
}

locals {
  namespace       = coalesce(var.namespace, var.service)
  service_account = coalesce(var.kubernetes_service_account, var.service)

  labels = merge(var.labels, {
    app         = var.service
    environment = var.environment
    managed-by  = "terraform"
  })

  # Direct Workload Identity Federation: IAM is granted to the Kubernetes
  # service account itself, so there is no Google service account and no key.
  workload_principal = format(
    "principal://iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s.svc.id.goog/subject/ns/%s/sa/%s",
    data.google_project.this.number, var.project_id, local.namespace, local.service_account,
  )
}

resource "google_project_service" "secretmanager" {
  project            = var.project_id
  service            = "secretmanager.googleapis.com"
  disable_on_destroy = false
}

resource "google_secret_manager_secret" "this" {
  for_each  = var.secrets
  project   = var.project_id
  secret_id = each.value
  labels    = local.labels
  replication {
    auto {}
  }
  depends_on = [google_project_service.secretmanager]
}

resource "google_secret_manager_secret_iam_member" "this" {
  for_each  = var.secrets
  project   = var.project_id
  secret_id = google_secret_manager_secret.this[each.key].secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = local.workload_principal
}

# The service's token for air-model-gateway lives in a secret the gateway's
# module owns (bootstrap-gateway-secrets.sh fills it). This grants read only.
resource "google_secret_manager_secret_iam_member" "gateway_token" {
  count     = var.gateway_token_secret_id == null ? 0 : 1
  project   = var.project_id
  secret_id = var.gateway_token_secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = local.workload_principal
}
