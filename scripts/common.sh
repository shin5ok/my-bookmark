#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
# The local .env is a trusted shell-format configuration file.
if [[ -f .env ]]; then set -a; source .env; set +a; fi
PROJECT_ID=${PROJECT_ID:-}
REGION=${REGION:-asia-northeast1}
SERVICE=${SERVICE:-shiori}
if [[ ! "$PROJECT_ID" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ || "$PROJECT_ID" == demo-* ]]; then
  echo 'Set PROJECT_ID to your billing-enabled GCP project in .env.' >&2; exit 1
fi
if [[ ! "$SERVICE" =~ ^[a-z][a-z0-9-]{0,39}$ || ! "$REGION" =~ ^[a-z]+-[a-z]+[0-9]+$ ]]; then
  echo 'Invalid SERVICE or REGION.' >&2; exit 1
fi
RUNTIME_SA="${SERVICE}-runtime@${PROJECT_ID}.iam.gserviceaccount.com"
BUILD_SA="${SERVICE}-build@${PROJECT_ID}.iam.gserviceaccount.com"
# Service-account IDs must be at most 30 characters.
if (( ${#SERVICE} > 22 )); then echo 'SERVICE must be at most 22 characters.' >&2; exit 1; fi
OAUTH_SECRET="${SERVICE}-google-client-secret"
