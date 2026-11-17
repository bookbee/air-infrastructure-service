output "address" {
  description = "The front door's private IP: what the hostname's DNS record points at."
  value       = google_compute_address.front_door.address
}

output "address_name" {
  description = "gateway.addressName in the orchestrator's values file."
  value       = google_compute_address.front_door.name
}

output "security_policy" {
  description = "gateway.securityPolicy in the orchestrator's values file."
  value       = google_compute_region_security_policy.front_door.name
}

output "proxy_only_cidr" {
  description = "Must be listed in networkPolicy.allowedCidrs of the orchestrator's values file."
  value       = var.proxy_only_cidr
}
