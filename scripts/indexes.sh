#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
# Keep this list in sync with firestore.indexes.json. Ignore only ALREADY_EXISTS.
create_index() {
  local collection=$1 field=$2 order=$3 direction=${4:-descending} output
  if output=$(gcloud firestore indexes composite create --project="$PROJECT_ID" --collection-group="$collection" --query-scope=collection \
    --field-config="field-path=$field,order=ascending" --field-config="field-path=$order,order=$direction" --quiet 2>&1); then
    printf '%s\n' "$output"
  elif [[ "$output" == *ALREADY_EXISTS* || "$output" == *'already exists'* ]]; then
    printf 'Index already exists: %s (%s, %s)\n' "$collection" "$field" "$order"
  else printf '%s\n' "$output" >&2; return 1; fi
}
create_index articles active created_at
create_index articles active count
create_index bookmarks user_id created_at
create_index bookmarks article_id created_at
create_index summary_jobs status queued_at ascending
