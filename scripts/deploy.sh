#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
: "${GOOGLE_CLIENT_ID:?Set GOOGLE_CLIENT_ID in .env}"
GEMINI_MODEL=${GEMINI_MODEL:-gemini-3.8-flash}
project_number=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
service_url="https://${SERVICE}-${project_number}.${REGION}.run.app"
base_url=${DEPLOY_BASE_URL:-$service_url}
if [[ "$base_url" != https://* || "$base_url" == *','* || "$base_url" == */ ]]; then echo 'DEPLOY_BASE_URL must be an HTTPS origin without a trailing slash.' >&2; exit 1; fi
for secret in "$OAUTH_SECRET" "$GEMINI_SECRET"; do
  gcloud secrets versions describe latest --secret="$secret" --project="$PROJECT_ID" >/dev/null
done
gcloud run deploy "$SERVICE" --source=. --project="$PROJECT_ID" --region="$REGION" \
  --service-account="$RUNTIME_SA" --build-service-account="projects/$PROJECT_ID/serviceAccounts/$BUILD_SA" \
  --allow-unauthenticated --port=8080 --cpu=2 --memory=2Gi --concurrency=40 --timeout=300 --no-cpu-throttling \
  --min=1 --max=3 \
  --set-env-vars="APP_ENV=production,GOOGLE_CLOUD_PROJECT=$PROJECT_ID,BASE_URL=$base_url,GOOGLE_CLIENT_ID=$GOOGLE_CLIENT_ID,GEMINI_MODEL=$GEMINI_MODEL" \
  --set-secrets="GOOGLE_CLIENT_SECRET=$OAUTH_SECRET:latest,GEMINI_API_KEY=$GEMINI_SECRET:latest" --quiet
printf '\nURL: %s\nGoogle OAuth redirect URI: %s/auth/google/callback\n' "$base_url" "$base_url"
