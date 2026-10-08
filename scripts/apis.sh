#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
enabled_services=$(gcloud services list --enabled --project="$PROJECT_ID" --format='value(config.name)')
missing_services=()
for api in run.googleapis.com firestore.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com secretmanager.googleapis.com iam.googleapis.com aiplatform.googleapis.com iap.googleapis.com cloudtasks.googleapis.com; do
  if ! grep -Fxq "$api" <<<"$enabled_services"; then missing_services+=("$api"); fi
done
if (( ${#missing_services[@]} )); then
  gcloud services enable "${missing_services[@]}" --project="$PROJECT_ID"
fi
if (( ${#missing_services[@]} )); then
  echo "Required APIs enabled for $PROJECT_ID."
else
  echo "Required APIs are already enabled for $PROJECT_ID."
fi
