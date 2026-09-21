#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
gcloud services enable run.googleapis.com firestore.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com iam.googleapis.com --project="$PROJECT_ID"
if ! gcloud firestore databases describe --database='(default)' --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud firestore databases create --database='(default)' --location="$REGION" --type=firestore-native --project="$PROJECT_ID"
fi
for kind in runtime build; do
  sa="${SERVICE}-${kind}@${PROJECT_ID}.iam.gserviceaccount.com"
  if ! gcloud iam service-accounts describe "$sa" --project="$PROJECT_ID" >/dev/null 2>&1; then
    gcloud iam service-accounts create "${SERVICE}-${kind}" --display-name="Shiori ${kind}" --project="$PROJECT_ID"
  fi
done
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$RUNTIME_SA" --role=roles/datastore.user --condition=None --quiet >/dev/null
gcloud projects add-iam-policy-binding "$PROJECT_ID" --member="serviceAccount:$BUILD_SA" --role=roles/run.builder --condition=None --quiet >/dev/null
for secret in "$OAUTH_SECRET" "$GEMINI_SECRET"; do
  if ! gcloud secrets describe "$secret" --project="$PROJECT_ID" >/dev/null 2>&1; then
    gcloud secrets create "$secret" --replication-policy=automatic --project="$PROJECT_ID"
  fi
  gcloud secrets add-iam-policy-binding "$secret" --member="serviceAccount:$RUNTIME_SA" --role=roles/secretmanager.secretAccessor --project="$PROJECT_ID" --quiet >/dev/null
done
for collection in sessions oauth_states quotas; do
  gcloud firestore fields ttls update expires_at --collection-group="$collection" --enable-ttl --project="$PROJECT_ID" --quiet
done
bash scripts/indexes.sh
echo 'Infrastructure prepared. Next: configure OAuth, then make secrets and make deploy.'
