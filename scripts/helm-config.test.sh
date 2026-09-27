#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHART_DIR="$ROOT_DIR/deploy/helm/multica"

require_rendered_value() {
  local rendered=$1
  local expected=$2

  if ! grep -Fq "$expected" <<<"$rendered"; then
    echo "Missing expected Helm-rendered config value:"
    echo "  $expected"
    exit 1
  fi
}

reject_rendered_value() {
  local rendered=$1
  local forbidden=$2

  if grep -Fq "$forbidden" <<<"$rendered"; then
    echo "Forbidden Helm-rendered config value:"
    echo "  $forbidden"
    exit 1
  fi
}

helm lint "$CHART_DIR"

default_config="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/configmap.yaml
)"
require_rendered_value "$default_config" 'MULTICA_VCS_INTEGRATION_ENABLED: "true"'
require_rendered_value "$default_config" 'MULTICA_CLOUD_URL: ""'
require_rendered_value "$default_config" 'MULTICA_DATABASE_STARTUP_TIMEOUT: "3m"'
require_rendered_value "$default_config" 'MULTICA_DATABASE_CONNECT_TIMEOUT: "5s"'
require_rendered_value "$default_config" 'MULTICA_BACKUP_ENABLED: "true"'
require_rendered_value "$default_config" 'MULTICA_BACKUP_DIR: "/app/backups"'
require_rendered_value "$default_config" 'MULTICA_BACKUP_RETENTION_DAYS: "7"'

default_backend="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/backend.yaml
)"
require_rendered_value "$default_backend" 'failureThreshold: 60'
require_rendered_value "$default_backend" 'name: multica-backend-backups'
require_rendered_value "$default_backend" 'helm.sh/resource-policy: keep'
require_rendered_value "$default_backend" 'mountPath: "/app/backups"'
require_rendered_value "$default_backend" 'claimName: multica-backend-backups'
liveness_block="$(sed -n '/livenessProbe:/,/resources:/p' <<<"$default_backend")"
require_rendered_value "$liveness_block" 'path: /health'
reject_rendered_value "$liveness_block" 'path: /healthz'

disabled_config="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/configmap.yaml \
    --set backend.config.vcsIntegrationEnabled=false
)"
require_rendered_value "$disabled_config" 'MULTICA_VCS_INTEGRATION_ENABLED: "false"'

backup_config="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/configmap.yaml \
    --set backend.backups.retentionDays=14 \
    --set backend.backups.path=/var/backups/multica
)"
require_rendered_value "$backup_config" 'MULTICA_BACKUP_RETENTION_DAYS: "14"'
require_rendered_value "$backup_config" 'MULTICA_BACKUP_DIR: "/var/backups/multica"'
backup_backend="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/backend.yaml \
    --set backend.backups.path=/var/backups/multica
)"
require_rendered_value "$backup_backend" 'mountPath: "/var/backups/multica"'

disabled_backup_config="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/configmap.yaml \
    --set backend.backups.enabled=false
)"
require_rendered_value "$disabled_backup_config" 'MULTICA_BACKUP_ENABLED: "false"'
disabled_backup_backend="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/backend.yaml \
    --set backend.backups.enabled=false
)"
reject_rendered_value "$disabled_backup_backend" 'claimName: multica-backend-backups'

no_upload_backend="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/backend.yaml \
    --set backend.uploads.persistence.enabled=false
)"
require_rendered_value "$no_upload_backend" 'claimName: multica-backend-backups'
reject_rendered_value "$no_upload_backend" 'claimName: multica-backend-uploads'

capacity_config="$(
  helm template multica "$CHART_DIR" \
    --show-only templates/configmap.yaml \
    --set-string backend.config.cloud.url=https://multica-cloud.internal
)"
require_rendered_value "$capacity_config" 'MULTICA_CLOUD_URL: "https://multica-cloud.internal"'

echo "helm config rendering ok"
