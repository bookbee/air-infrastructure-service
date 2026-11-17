variable "project_id" {
  type = string
}

variable "region" {
  description = "Region of the GKE cluster. An internal Application Load Balancer is regional, and its clients must be in this region and VPC (or reach it over VPN, Interconnect or peering)."
  type        = string
}

variable "environment" {
  description = "uat or prod; used in labels and names."
  type        = string
  validation {
    condition     = contains(["uat", "prod"], var.environment)
    error_message = "environment must be uat or prod."
  }
}

variable "name" {
  description = "Prefix for the address, the subnet and the security policy."
  type        = string
  default     = "air-front-door"
}

variable "network_id" {
  description = "VPC the cluster runs in (projects/<p>/global/networks/<n>)."
  type        = string
}

variable "subnetwork_id" {
  description = "The cluster's subnet (projects/<p>/regions/<r>/subnetworks/<n>). The private address is reserved in it."
  type        = string
}

variable "create_proxy_only_subnet" {
  description = "Create the proxy-only subnet an internal Application Load Balancer needs. false when the VPC already has one for this region (there can be only one active per region and VPC)."
  type        = bool
  default     = true
}

variable "proxy_only_cidr" {
  description = "Range of the proxy-only subnet. The load balancer's proxies reach the pods from this range, so it must also be in networkPolicy.allowedCidrs of the orchestrator's values file. /26 or larger; /23 is recommended."
  type        = string
  default     = "10.129.0.0/23"
}

variable "rate_limit_count" {
  description = "Requests allowed per caller in each interval before the front door answers 429 (orchestrator spec 0016, REQ-FDR-012)."
  type        = number
  default     = 300
}

variable "rate_limit_interval_sec" {
  description = "Length of the rate-limit interval. Cloud Armor accepts 10, 30, 60, 120, 180, 240, 300, 600, 900, 1200, 1800, 2700 or 3600."
  type        = number
  default     = 60
}

variable "rate_limit_enforce_on_key" {
  description = "What a 'caller' is for the rate limit: IP (the source address: one shop backend) or HTTP_HEADER (see rate_limit_header_name)."
  type        = string
  default     = "IP"
  validation {
    condition     = contains(["IP", "HTTP_HEADER", "ALL"], var.rate_limit_enforce_on_key)
    error_message = "rate_limit_enforce_on_key must be IP, HTTP_HEADER or ALL."
  }
}

variable "rate_limit_header_name" {
  description = "With enforce_on_key = HTTP_HEADER: the header whose value identifies the caller. X-API-Key limits each client application separately; Cloud Armor uses the value only as a counter key."
  type        = string
  default     = "X-API-Key"
}

variable "labels" {
  type    = map(string)
  default = {}
}
