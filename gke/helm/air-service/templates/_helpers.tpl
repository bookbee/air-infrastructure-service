{{- define "air.name" -}}
{{- default .Release.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* The release name is the service name, so resources are named after the service. */}}
{{- define "air.fullname" -}}
{{- default .Release.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "air.selectorLabels" -}}
app.kubernetes.io/name: {{ include "air.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "air.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "air.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.image.tag | default "digest" | quote }}
app.kubernetes.io/part-of: air-platform
app.kubernetes.io/managed-by: {{ .Release.Service }}
air-platform/environment: {{ .Values.environment }}
{{- end }}

{{- define "air.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "air.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "air.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- end }}
{{- end }}

{{- define "air.validate" -}}
{{- if not .Values.image.repository }}
{{- fail "image.repository is required: pass -f gke/values/<env>/<service>.yaml" }}
{{- end }}
{{- if or (contains "<" .Values.image.repository) (contains "<" (toString .Values.secrets.gcp.project)) }}
{{- fail "image.repository or secrets.gcp.project still holds a <placeholder>: fill gke/values/<env>/<service>.yaml or pass IMAGE_REPOSITORY / GCP_PROJECT" }}
{{- end }}
{{- if and (not .Values.image.digest) (not .Values.image.tag) }}
{{- fail "set image.digest or image.tag: this chart has no default image version" }}
{{- end }}
{{- if not (has .Values.environment (list "uat" "prod" "kind")) }}
{{- fail (printf "environment must be uat, prod or kind, got %q" .Values.environment) }}
{{- end }}
{{- if not (has .Values.strategy (list "RollingUpdate" "Recreate")) }}
{{- fail (printf "strategy must be RollingUpdate or Recreate, got %q" .Values.strategy) }}
{{- end }}
{{- if not (has .Values.secrets.mode (list "gcpSecretManager" "kubernetesSecret" "none")) }}
{{- fail (printf "secrets.mode must be gcpSecretManager, kubernetesSecret or none, got %q" .Values.secrets.mode) }}
{{- end }}
{{- if and (ne .Values.secrets.mode "none") (not .Values.command) }}
{{- fail "command is required unless secrets.mode=none: the secrets wrapper execs it (copy the image's ENTRYPOINT/CMD)" }}
{{- end }}
{{- if and (eq .Values.secrets.mode "gcpSecretManager") (not .Values.secrets.gcp.project) }}
{{- fail "secrets.gcp.project is required when secrets.mode=gcpSecretManager" }}
{{- end }}
{{- if and (eq .Values.secrets.mode "gcpSecretManager") (not .Values.secrets.gcp.items) }}
{{- fail "secrets.gcp.items is empty: list the service's secrets, or set secrets.mode=none" }}
{{- end }}
{{- if and (eq .Values.secrets.mode "kubernetesSecret") (not .Values.secrets.kubernetesSecret.name) }}
{{- fail "secrets.kubernetesSecret.name is required when secrets.mode=kubernetesSecret" }}
{{- end }}
{{- if and (eq .Values.strategy "Recreate") .Values.autoscaling.enabled }}
{{- fail "strategy=Recreate is for a service that must run as one copy; it cannot be combined with autoscaling" }}
{{- end }}
{{- if le (int .Values.terminationGracePeriodSeconds) (int .Values.preStopSleepSeconds) }}
{{- fail "terminationGracePeriodSeconds must exceed preStopSleepSeconds" }}
{{- end }}
{{- end }}
