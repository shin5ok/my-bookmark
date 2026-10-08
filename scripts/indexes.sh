#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
command -v jq >/dev/null || { echo 'jq is required for make indexes.' >&2; exit 1; }
# Keep this list in sync with firestore.indexes.json.
indexes_json=$(gcloud firestore indexes composite list --project="$PROJECT_ID" --format=json)
jq -e 'type == "array"' <<<"$indexes_json" >/dev/null
create_index() {
  local collection=$1 field=$2 order=$3 direction=${4:-descending} output
  if jq -e --arg collection "$collection" --arg field "$field" --arg order "$order" --arg direction "$direction" '
    any(.[];
      (.collectionGroup // ((.name // "") | split("/collectionGroups/") | .[1] // "" | split("/") | .[0])) == $collection
      and .queryScope == "COLLECTION"
      and .state != "DELETING"
      and .fields[0].fieldPath == $field and .fields[0].order == "ASCENDING"
      and .fields[1].fieldPath == $order and .fields[1].order == ($direction | ascii_upcase)
    )
  ' <<<"$indexes_json" >/dev/null; then
    printf 'Index already exists: %s (%s, %s)\n' "$collection" "$field" "$order"
    return 0
  fi
  if output=$(gcloud firestore indexes composite create --project="$PROJECT_ID" --collection-group="$collection" --query-scope=collection \
    --field-config="field-path=$field,order=ascending" --field-config="field-path=$order,order=$direction" --quiet 2>&1); then
    printf '%s\n' "$output"
  elif [[ "$output" == *ALREADY_EXISTS* || "$output" == *'already exists'* ]]; then
    printf 'Index already exists: %s (%s, %s)\n' "$collection" "$field" "$order"
  else printf '%s\n' "$output" >&2; return 1; fi
}
create_index articles active created_at
create_index articles active count
create_index articles active rating_total
create_index bookmarks user_id created_at
create_index bookmarks article_id created_at
create_index summary_jobs status queued_at ascending
