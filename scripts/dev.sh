#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
if [[ ! -f .env ]]; then echo 'Run: cp .env.example .env' >&2; exit 1; fi
set -a; source .env; set +a
exec go run ./cmd/server
