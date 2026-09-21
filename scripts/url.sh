#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
project_number=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
service_url="https://${SERVICE}-${project_number}.${REGION}.run.app"
base_url=${DEPLOY_BASE_URL:-$service_url}
printf 'URL: %s\nOAuth redirect URI: %s/auth/google/callback\n' "$base_url" "$base_url"
