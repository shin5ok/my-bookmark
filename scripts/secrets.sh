#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
: "${GOOGLE_CLIENT_SECRET:?Set GOOGLE_CLIENT_SECRET in .env}"
if ! gcloud secrets describe "$OAUTH_SECRET" --project="$PROJECT_ID" >/dev/null 2>&1; then
  gcloud secrets create "$OAUTH_SECRET" --replication-policy=automatic --project="$PROJECT_ID"
fi
versions=$(gcloud secrets versions list "$OAUTH_SECRET" --project="$PROJECT_ID" --limit=1 --format='value(name)')
if [[ -n "$versions" ]]; then
  current_hash=$(gcloud secrets versions access latest --secret="$OAUTH_SECRET" --project="$PROJECT_ID" | shasum -a 256)
  desired_hash=$(printf '%s' "$GOOGLE_CLIENT_SECRET" | shasum -a 256)
  if [[ "$current_hash" == "$desired_hash" ]]; then
    echo "Secret $OAUTH_SECRET is unchanged."
    exit 0
  fi
fi
printf '%s' "$GOOGLE_CLIENT_SECRET" | gcloud secrets versions add "$OAUTH_SECRET" --data-file=- --project="$PROJECT_ID"
