SHELL := /bin/bash
.DEFAULT_GOAL := help
export GOCACHE := $(CURDIR)/.cache/go-build
export GOPATH := $(CURDIR)/.cache/go
export PROJECT_ID REGION SERVICE BASE_URL GOOGLE_CLIENT_ID GEMINI_MODEL GEMINI_LOCATION

.PHONY: help dev fmt vet test test-integration build clean emulator emulator-stop bootstrap secrets indexes deploy url preview
help: ## コマンド一覧
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
dev: ## .envを読み込んでローカル起動（Emulatorが必要）
	@bash scripts/dev.sh
fmt: ## Goのコード整形
	go fmt ./...
vet: ## 静的解析
	go vet ./...
test: ## 単体テスト（race検出）
	go test -race ./...
test-integration: ## 起動済みFirestore Emulatorで統合テスト
	FIRESTORE_EMULATOR_HOST=127.0.0.1:8085 go test -race -count=1 ./internal/store ./internal/app
build: ## 実行ファイルをbin/serverにビルド
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/server ./cmd/server
clean: ## ビルド出力を削除
	rm -f bin/server
emulator: ## Firestore EmulatorをDockerで起動
	docker-compose up -d --wait firestore
emulator-stop: ## Emulatorを停止（データは一時的）
	docker-compose down
bootstrap: ## GCPのAPI・DB・実行用サービスアカウント・Secretを初期構築
	@bash scripts/bootstrap.sh
secrets: ## .envのOAuthシークレットをSecret Managerに登録
	@bash scripts/secrets.sh
indexes: ## Firestoreの複合インデックスを作成
	@bash scripts/indexes.sh
deploy: ## テスト・静的解析後、Cloud BuildのbuildpacksでビルドしてCloud Runへデプロイ
	$(MAKE) test vet
	@bash scripts/deploy.sh
url: ## IAPで保護されたCloud RunのURLを表示
	@bash scripts/url.sh
preview: ## テスト用サンプル記事で画面確認（本番機能・外部APIなし）
	SHIORI_PREVIEW=1 go test ./internal/app -run '^TestPreviewServer$$' -count=1 -timeout=30m
