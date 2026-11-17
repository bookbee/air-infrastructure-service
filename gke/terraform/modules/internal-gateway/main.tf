# The GCP side of air-orchestrator-service's front door: an internal
# Application Load Balancer, created by GKE from the Gateway in the service's
# Helm release (gateway.* in gke/values/<env>/air-orchestrator-service.yaml).
# The contract it implements is the orchestrator's spec 0016.
#
# GKE creates the load balancer itself. What it can't create, and this module
# does, are the three things the Gateway refers to by name or relies on:
#   1. the proxy-only subnet every internal Application Load Balancer needs,
#   2. a reserved private address, so the front door's IP survives a redeploy,
#   3. the Cloud Armor policy that rate-limits callers.
# The DNS name and the TLS certificate are the platform's own (runbook §5.6).

locals {
  labels = merge(var.labels, {
    app         = "air-orchestrator-service"
    environment = var.environment
    managed-by  = "terraform"
  })
}

# Envoy proxies for regional load balancers live here. Traffic reaches the pods
# from this range, not from the client's address.
resource "google_compute_subnetwork" "proxy_only" {
  count         = var.create_proxy_only_subnet ? 1 : 0
  project       = var.project_id
  region        = var.region
  name          = "${var.name}-proxy-only-${var.region}"
  network       = var.network_id
  ip_cidr_range = var.proxy_only_cidr
  purpose       = "REGIONAL_MANAGED_PROXY"
  role          = "ACTIVE"
}

# The front door's address: private, in the cluster's subnet. The Gateway
# refers to it by name (gateway.addressName).
resource "google_compute_address" "front_door" {
  project      = var.project_id
  region       = var.region
  name         = "${var.name}-${var.environment}"
  description  = "air-orchestrator-service front door (${var.environment}), internal Application Load Balancer"
  address_type = "INTERNAL"
  purpose      = "SHARED_LOADBALANCER_VIP"
  subnetwork   = var.subnetwork_id
  labels       = local.labels
}

# A regional policy: a global one can't be attached to a regional load
# balancer. The Gateway's backend policy refers to it by name
# (gateway.securityPolicy).
resource "google_compute_region_security_policy" "front_door" {
  project     = var.project_id
  region      = var.region
  name        = "${var.name}-${var.environment}"
  description = "air-orchestrator-service front door: rate limit per caller (spec 0016, REQ-FDR-012)"
  type        = "CLOUD_ARMOR"
}

# Every caller is allowed up to the threshold, then answered 429 until the
# interval rolls over. The orchestrator's own rate limit is not enforced yet
# (air-web-console OR-017), so this is the only one.
resource "google_compute_region_security_policy_rule" "rate_limit" {
  project         = var.project_id
  region          = var.region
  security_policy = google_compute_region_security_policy.front_door.name
  description     = "Throttle each caller"
  priority        = 1000
  action          = "throttle"

  match {
    versioned_expr = "SRC_IPS_V1"
    config {
      src_ip_ranges = ["*"]
    }
  }

  rate_limit_options {
    conform_action = "allow"
    exceed_action  = "deny(429)"
    enforce_on_key = var.rate_limit_enforce_on_key
    # Only read when enforce_on_key is HTTP_HEADER.
    enforce_on_key_name = var.rate_limit_enforce_on_key == "HTTP_HEADER" ? var.rate_limit_header_name : null

    rate_limit_threshold {
      count        = var.rate_limit_count
      interval_sec = var.rate_limit_interval_sec
    }
  }
}
