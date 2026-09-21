#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
: "${GOOGLE_CLIENT_SECRET:?Set GOOGLE_CLIENT_SECRET in .env}"
: "${GEMINI_API_KEY:?Set GEMINI_API_KEY in .env}"
printf '%s' "$GOOGLE_CLIENT_SECRET" | gcloud secrets versions add "$OAUTH_SECRET" --data-file=- --project="$PROJECT_ID"
printf '%s' "$GEMINI_API_KEY" | gcloud secrets versions add "$GEMINI_SECRET" --data-file=- --project="$PROJECT_ID"
