# Repository Guidelines

## Project Structure & Module Organization

- `cmd/server/`: process startup, dependency wiring, and graceful shutdown.
- `internal/app/`: chi routes, OAuth/OIDC, CSRF, handlers, and summary jobs.
- `internal/content/`: safe URL fetching, article extraction, Chromium fallback, and Vertex AI Gemini calls.
- `internal/store/`: Firestore models, transactions, sessions, quotas, and job leases.
- `internal/web/`: embedded templates, CSS, and JavaScript.
- `scripts/`: local development, Google Cloud setup, secrets, indexes, and deployment.
- `docs/superpowers/`: design notes and implementation plans.

Keep tests beside the code they cover as `*_test.go` files.

## Build, Test, and Development Commands

- `make emulator`: start the Firestore Emulator.
- `make dev`: load `.env` and run the service on localhost.
- `make test`: run unit tests with the race detector.
- `make test-integration`: run Firestore integration tests against the active emulator.
- `make fmt` / `make vet`: format and statically analyze Go packages.
- `make build`: create the static `bin/server` binary.
- `make preview`: serve sample UI data on port 8090.

Run `make test vet build` before submitting substantial changes.

## Coding Style & Naming Conventions

Use `gofmt` through `make fmt`. Follow idiomatic Go naming: exported identifiers use `PascalCase`; unexported identifiers use `camelCase`. Keep packages focused on existing responsibilities and prefer small interfaces at boundaries. Handle errors explicitly and wrap dependency errors with useful context. Preserve security checks around URL fetching, authentication, CSRF, escaping, and job ownership.

## Testing Guidelines

Use Go's `testing` package. Name tests `TestBehavior` and use table-driven subtests for related cases. Add regression tests for fixes and boundary tests for authentication, validation, quotas, transactions, and SSRF protections. Do not depend on live websites, Vertex AI, or production credentials; use local HTTP fixtures, stubs, and the Firestore Emulator.

## Commit & Pull Request Guidelines

History is too small to establish a strict convention. Use a short imperative subject, such as `Use ADC for Vertex AI authentication`, and keep commits focused. Pull requests should explain behavior, risks, configuration or IAM impact, and verification commands. Link relevant issues and include desktop and mobile screenshots for UI changes.

## Security & Configuration

Copy `.env.example` to `.env`, but never commit credentials. Gemini uses Vertex AI with Application Default Credentials, not API keys; locally run `gcloud auth application-default login`. Treat fetched article content as untrusted input. Do not weaken SSRF restrictions or expose Firestore directly to browsers.
