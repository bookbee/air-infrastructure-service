#!/bin/sh
# Renders or deploys one AIR service's Helm release for one environment. The
# Makefile's gke-render / gke-deploy targets call this; so does validate.sh.
# Keeping the Helm arguments here means a render, a validation and a deploy
# can't drift apart.
#
#   gke/scripts/release.sh render <service> <uat|prod|kind>
#   gke/scripts/release.sh deploy <service> <uat|prod>
#
#   IMAGE_REPOSITORY  registry path of the image; replaces the <placeholder>
#   IMAGE_DIGEST      sha256:... (preferred), or
#   IMAGE_TAG         a tag
#   GCP_PROJECT       project holding the secrets; replaces the <placeholder>
#   KUBE_CONTEXT      deploy only, required: never defaulted, so a deploy can't
#                     land on whatever cluster kubectl happens to point at
#   GATEWAY_DIR       the air-model-gateway checkout (default: next to this
#                     repository). Its routing, pricing and policy files are
#                     passed to the gateway's chart from there.
#   SAMPLE=1          render only: stand-ins for the placeholders, so the
#                     manifests can be validated without a real project
#   KUBE_VERSION      Kubernetes version to render for (default 1.33.0)
set -eu

action=${1:?usage: release.sh render|deploy <service> <env>}
service=${2:?usage: release.sh render|deploy <service> <env>}
env=${3:?usage: release.sh render|deploy <service> <env>}
gke=$(cd "$(dirname "$0")/.." && pwd)
gateway_dir=${GATEWAY_DIR:-$gke/../../air-model-gateway}

case "$env" in uat|prod|kind) ;; *) echo "environment must be uat, prod or kind, got '$env'" >&2; exit 2;; esac
case "$service" in
  air-model-gateway) chart="$gke/helm/air-model-gateway" ;;
  air-classifier-service|air-rag-service|air-tools-gateway|air-orchestrator-service) chart="$gke/helm/air-service" ;;
  *) echo "unknown service '$service' (air-model-gateway, air-classifier-service, air-rag-service, air-tools-gateway, air-orchestrator-service)" >&2; exit 2 ;;
esac

# kind values are an overlay on production's for the shared chart, and a
# complete file for the gateway's own chart.
set --
if [ "$env" = kind ] && [ "$service" != air-model-gateway ]; then
  set -- -f "$gke/values/prod/$service.yaml" -f "$gke/values/kind/$service.yaml"
else
  set -- -f "$gke/values/$env/$service.yaml"
fi

if [ "$service" = air-model-gateway ]; then
  # The gateway calls its pre-production environment `staging`; kind runs the
  # production policy.
  case "$env" in uat) policy=staging ;; *) policy=prod ;; esac
  for f in deploy/routing.yaml deploy/pricing.yaml "deploy/policy/policy.$policy.yaml"; do
    [ -f "$gateway_dir/$f" ] || { echo "missing $gateway_dir/$f: set GATEWAY_DIR to the air-model-gateway checkout" >&2; exit 1; }
  done
  set -- "$@" \
    --set-file "config.routing=$gateway_dir/deploy/routing.yaml" \
    --set-file "config.pricing=$gateway_dir/deploy/pricing.yaml" \
    --set-file "config.policy=$gateway_dir/deploy/policy/policy.$policy.yaml"
fi

if [ "${SAMPLE:-}" = 1 ] && [ "$env" != kind ]; then
  set -- "$@" --set "image.repository=us-central1-docker.pkg.dev/sample/air/$service" \
    --set image.tag=sample --set secrets.gcp.project=sample
fi
[ -z "${IMAGE_REPOSITORY:-}" ] || set -- "$@" --set "image.repository=$IMAGE_REPOSITORY"
[ -z "${IMAGE_DIGEST:-}" ] || set -- "$@" --set "image.digest=$IMAGE_DIGEST"
[ -z "${IMAGE_TAG:-}" ] || set -- "$@" --set "image.tag=$IMAGE_TAG"
[ -z "${GCP_PROJECT:-}" ] || set -- "$@" --set "secrets.gcp.project=$GCP_PROJECT"

case "$action" in
  render)
    helm template "$service" "$chart" -n "$service" --kube-version "${KUBE_VERSION:-1.33.0}" "$@" ;;
  deploy)
    [ "$env" != kind ] || { echo "kind is deployed with 'make kind-up'" >&2; exit 2; }
    : "${KUBE_CONTEXT:?set KUBE_CONTEXT (see docs/03-gke-runbook.md)}"
    if [ -z "${IMAGE_DIGEST:-}" ] && [ -z "${IMAGE_TAG:-}" ] && [ "$service" != air-model-gateway ]; then
      echo "set IMAGE_DIGEST (preferred) or IMAGE_TAG: the shared chart has no default image version" >&2; exit 2
    fi
    helm --kube-context "$KUBE_CONTEXT" upgrade --install "$service" "$chart" \
      -n "$service" --create-namespace "$@" --atomic --wait --timeout 10m ;;
  *) echo "usage: release.sh render|deploy <service> <env>" >&2; exit 2 ;;
esac
