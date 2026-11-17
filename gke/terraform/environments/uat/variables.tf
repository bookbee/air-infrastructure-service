variable "project_id" {
  description = "The UAT project. One project per environment."
  type        = string
}

variable "region" {
  type    = string
  default = "us-central1"
}

variable "create_platform" {
  description = "Build the VPC, Autopilot cluster and Artifact Registry (modules/gke-platform). false when the platform team already runs them."
  type        = bool
  default     = true
}

variable "network_id" {
  description = "Existing VPC (projects/<p>/global/networks/<n>) when create_platform = false."
  type        = string
  default     = null
}

variable "master_authorized_networks" {
  type = list(object({
    cidr_block   = string
    display_name = string
  }))
  default = []
}

variable "subnetwork_id" {
  description = "The cluster's subnet (projects/<p>/regions/<r>/subnetworks/<n>) when create_platform = false. The front door's private address is reserved in it."
  type        = string
  default     = null
}

variable "create_proxy_only_subnet" {
  description = "false when the VPC already has a proxy-only subnet in this region; then set proxy_only_cidr to its range."
  type        = bool
  default     = true
}

variable "proxy_only_cidr" {
  description = "Range of the proxy-only subnet. Keep it in step with networkPolicy.allowedCidrs in gke/values/<env>/air-orchestrator-service.yaml."
  type        = string
  default     = "10.129.0.0/23"
}

variable "front_door_rate_limit_count" {
  description = "Requests per minute each caller may send to air-orchestrator-service before the front door answers 429."
  type        = number
  default     = 300
}
