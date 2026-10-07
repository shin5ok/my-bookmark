#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
command -v jq >/dev/null || { echo 'jq is required for make bootstrap.' >&2; exit 1; }
enabled_services=$(gcloud services list --enabled --project="$PROJECT_ID" --format='value(config.name)')
missing_services=()
for api in run.googleapis.com firestore.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com iam.googleapis.com aiplatform.googleapis.com iap.googleapis.com; do
  if ! grep -Fxq "$api" <<<"$enabled_services"; then missing_services+=("$api"); fi
done
if (( ${#missing_services[@]} )); then
  gcloud services enable "${missing_services[@]}" --project="$PROJECT_ID"
fi
if ! gcloud firestore databases describe --database='(default)' --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud firestore databases create --database='(default)' --location="$REGION" --type=firestore-native --project="$PROJECT_ID"
fi
for kind in runtime build; do
  sa="${SERVICE}-${kind}@${PROJECT_ID}.iam.gserviceaccount.com"
  if ! gcloud iam service-accounts describe "$sa" --project="$PROJECT_ID" >/dev/null 2>&1; then
    gcloud iam service-accounts create "${SERVICE}-${kind}" --display-name="Shiori ${kind}" --project="$PROJECT_ID"
  fi
done
if ! gcloud artifacts repositories describe shiori-images --location="$REGION" --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud artifacts repositories create shiori-images --repository-format=docker --location="$REGION" --project="$PROJECT_ID"
fi
has_binding() {
  jq -e --arg role "$2" --arg member "$3" '
    any(.bindings[]?; .role == $role and .condition == null and (.members // [] | index($member) != null))
  ' <<<"$1" >/dev/null
}
project_policy=$(gcloud projects get-iam-policy "$PROJECT_ID" --format=json)
jq -e '(.bindings // []) | type == "array"' <<<"$project_policy" >/dev/null
for binding in "$RUNTIME_SA roles/datastore.user" "$RUNTIME_SA roles/aiplatform.user" "$BUILD_SA roles/run.builder"; do
  read -r sa role <<<"$binding"
  if ! has_binding "$project_policy" "$role" "serviceAccount:$sa"; then
    gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$sa" --role="$role" --condition=None --quiet >/dev/null
  fi
done
for secret in "$OAUTH_SECRET"; do
  if ! gcloud secrets describe "$secret" --project="$PROJECT_ID" >/dev/null 2>&1; then
    gcloud secrets create "$secret" --replication-policy=automatic --project="$PROJECT_ID"
  fi
  secret_policy=$(gcloud secrets get-iam-policy "$secret" --project="$PROJECT_ID" --format=json)
  jq -e '(.bindings // []) | type == "array"' <<<"$secret_policy" >/dev/null
  if ! has_binding "$secret_policy" roles/secretmanager.secretAccessor "serviceAccount:$RUNTIME_SA"; then
    gcloud secrets add-iam-policy-binding "$secret" --member="serviceAccount:$RUNTIME_SA" --role=roles/secretmanager.secretAccessor --project="$PROJECT_ID" --quiet >/dev/null
  fi
done
for collection in sessions oauth_states quotas; do
  ttl_fields=$(gcloud firestore fields ttls list --collection-group="$collection" --project="$PROJECT_ID" --format='value(name)')
  if ! grep -Fxq "projects/$PROJECT_ID/databases/(default)/collectionGroups/$collection/fields/expires_at" <<<"$ttl_fields"; then
    gcloud firestore fields ttls update expires_at --collection-group="$collection" --enable-ttl --project="$PROJECT_ID" --quiet
  fi
done
bash scripts/indexes.sh
echo 'Infrastructure prepared. Next: edit allow_accounts.yaml, then make deploy.'
