output "network_id" {
  value = google_compute_network.this.id
}

output "subnetwork_id" {
  description = "The cluster's subnet. The front door's private address is reserved in it."
  value       = google_compute_subnetwork.this.id
}

output "cluster_name" {
  value = google_container_cluster.this.name
}

output "cluster_location" {
  value = google_container_cluster.this.location
}

output "image_repository" {
  description = "Docker repository path for image.repository (append /air-model-gateway)."
  value       = var.artifact_registry_repository == null ? null : "${var.region}-docker.pkg.dev/${var.project_id}/${var.artifact_registry_repository}"
}
