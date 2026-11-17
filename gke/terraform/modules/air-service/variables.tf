variable "project_id" {
  description = "GCP project of this environment. One project per environment: secret names are fixed, so two environments can't share a project."
  type        = string
}

variable "environment" {
  description = "uat or prod; used in labels."
  type        = string
  validation {
    condition     = contains(["uat", "prod"], var.environment)
    error_message = "environment must be uat or prod."
  }
}

variable "service" {
  description = "The service's repository name, e.g. air-rag-service. Used for labels, and as the default namespace and Kubernetes service account."
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace the Helm release runs in. Defaults to the service name."
  type        = string
  default     = null
}

variable "kubernetes_service_account" {
  description = "Kubernetes service account of the release (the chart's default is the release name). Defaults to the service name."
  type        = string
  default     = null
}

variable "secrets" {
  description = "Secrets this service's pods read: setting name (the environment variable) -> Secret Manager secret id. Only empty containers and their IAM are created here. Values are added out of band (gcloud secrets versions add), never through Terraform, so they never land in state."
  type        = map(string)
}

variable "gateway_token_secret_id" {
  description = "Secret id of this service's air-model-gateway token (<service>-model-gateway-token, created by modules/air-model-gateway). null for a service that makes no model call."
  type        = string
  default     = null
}

variable "labels" {
  type    = map(string)
  default = {}
}
