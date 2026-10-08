#!/usr/bin/env bash
source "$(dirname "$0")/common.sh"
# Migration explicitly targets the deployment project, never the local emulator.
env -u FIRESTORE_EMULATOR_HOST go run ./cmd/migrate-ratings --project="$PROJECT_ID"
