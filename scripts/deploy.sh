#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
GEMINI_MODEL=${GEMINI_MODEL:-gemini-3.8-flash}
GEMINI_LOCATION=${GEMINI_LOCATION:-global}
project_number=$(gcloud projects describe "$PROJECT_ID" --format='value(projectNumber)')
service_url="https://${SERVICE}-${project_number}.${REGION}.run.app"
base_url=${DEPLOY_BASE_URL:-$service_url}
api_service="${SERVICE}-api"
api_url="https://${api_service}-${project_number}.${REGION}.run.app"
if [[ "$base_url" != https://* || "$base_url" == *','* || "$base_url" == */ ]]; then echo 'DEPLOY_BASE_URL must be an HTTPS origin without a trailing slash.' >&2; exit 1; fi
[[ -f allow_accounts.yaml ]] || { echo 'allow_accounts.yaml is required.' >&2; exit 1; }
go run ./cmd/iap-policy --validate allow_accounts.yaml
if ! gcloud artifacts repositories describe shiori-images --location="$REGION" --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud artifacts repositories create shiori-images --repository-format=docker --location="$REGION" --project="$PROJECT_ID"
fi
image="${REGION}-docker.pkg.dev/${PROJECT_ID}/shiori-images/${SERVICE}:$(date -u +%Y%m%d%H%M%S)-$$"
gcloud builds submit . --config=cloudbuild.yaml --project="$PROJECT_ID" --region="$REGION" \
  --service-account="projects/$PROJECT_ID/serviceAccounts/$BUILD_SA" \
  --substitutions="_IMAGE=$image" --quiet
# Tasks call a dedicated IAM-protected service; browser IAP is never bypassed.
worker_service="${SERVICE}-worker"
worker_url="https://${worker_service}-${project_number}.${REGION}.run.app"
queue="${SERVICE}-summary"
queue_resource="projects/$PROJECT_ID/locations/$REGION/queues/$queue"
queue_action=update
if ! gcloud tasks queues describe "$queue" --project="$PROJECT_ID" --location="$REGION" >/dev/null 2>&1; then queue_action=create; fi
gcloud tasks queues "$queue_action" "$queue" --project="$PROJECT_ID" --location="$REGION" \
  --max-concurrent-dispatches=3 --max-dispatches-per-second=1 --max-attempts=-1 \
  --min-backoff=30s --max-backoff=600s --max-retry-duration=0s --quiet
tasks_env="TASKS_QUEUE=$queue_resource,TASKS_WORKER_URL=$worker_url,TASKS_SERVICE_ACCOUNT=$TASKS_SA"
gcloud run deploy "$worker_service" --image="$image" --project="$PROJECT_ID" --region="$REGION" \
  --service-account="$RUNTIME_SA" \
  --no-allow-unauthenticated --no-iap --port=8080 --cpu=1 --memory=2Gi --concurrency=1 --timeout=660 --cpu-throttling \
  --min=0 --max=3 \
  --set-env-vars="APP_ENV=production,WORKER_ONLY=true,GOOGLE_CLOUD_PROJECT=$PROJECT_ID,BASE_URL=$worker_url,GEMINI_MODEL=$GEMINI_MODEL,GEMINI_LOCATION=$GEMINI_LOCATION,$tasks_env" \
  --clear-secrets --quiet
gcloud run services add-iam-policy-binding "$worker_service" --project="$PROJECT_ID" --region="$REGION" \
  --member="serviceAccount:$TASKS_SA" --role=roles/run.invoker --quiet >/dev/null
gcloud run deploy "$SERVICE" --image="$image" --project="$PROJECT_ID" --region="$REGION" \
  --service-account="$RUNTIME_SA" \
  --no-allow-unauthenticated --iap --port=8080 --cpu=1 --memory=2Gi --concurrency=40 --timeout=300 --no-cpu-throttling \
  --min=1 --max=3 \
  --set-env-vars="APP_ENV=production,GOOGLE_CLOUD_PROJECT=$PROJECT_ID,BASE_URL=$base_url,API_BASE_URL=$api_url,IAP_AUDIENCE=/projects/$project_number/locations/$REGION/services/$SERVICE,GEMINI_MODEL=$GEMINI_MODEL,GEMINI_LOCATION=$GEMINI_LOCATION,$tasks_env" \
  --clear-secrets --quiet
gcloud run services add-iam-policy-binding "$SERVICE" --project="$PROJECT_ID" --region="$REGION" \
  --member="serviceAccount:service-${project_number}@gcp-sa-iap.iam.gserviceaccount.com" \
  --role=roles/run.invoker --quiet >/dev/null
current_policy=$(mktemp)
desired_policy=$(mktemp)
trap 'rm -f "$current_policy" "$desired_policy"' EXIT
gcloud iap web get-iam-policy --project="$PROJECT_ID" --region="$REGION" --resource-type=cloud-run --service="$SERVICE" --format=json > "$current_policy"
go run ./cmd/iap-policy allow_accounts.yaml "$current_policy" "$desired_policy"
gcloud iap web set-iam-policy "$desired_policy" --project="$PROJECT_ID" --region="$REGION" --resource-type=cloud-run --service="$SERVICE" --quiet
printf '\nIAP URL: %s\n' "$base_url"

# Query tokens must not be retained in the default request log bucket.
exclusion_name="${SERVICE}-api-request-urls"
existing_exclusion=$(gcloud logging sinks list --project="$PROJECT_ID" \
  --filter="name=_Default AND exclusions.name=$exclusion_name" --format='value(name)')
exclusion_flag=--add-exclusion
if [[ "$existing_exclusion" == _Default ]]; then exclusion_flag=--update-exclusion; fi
gcloud logging sinks update _Default --project="$PROJECT_ID" \
  "$exclusion_flag=name=$exclusion_name,disabled=false,filter=resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"$api_service\" AND log_id(\"run.googleapis.com/requests\")" --quiet
# This service exposes only healthz and token-authenticated bookmark registration.
gcloud run deploy "$api_service" --image="$image" --project="$PROJECT_ID" --region="$REGION" \
  --service-account="$RUNTIME_SA" \
  --allow-unauthenticated --no-iap --port=8080 --cpu=1 --memory=512Mi --concurrency=40 --timeout=60 \
  --min=0 --max=3 \
  --set-env-vars="APP_ENV=production,API_ONLY=true,GOOGLE_CLOUD_PROJECT=$PROJECT_ID,BASE_URL=$base_url,API_BASE_URL=$api_url" \
  --clear-secrets --quiet
printf '\nToken API URL: %s/api/bookmarks\n' "$api_url"
