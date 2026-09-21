# しおり — AI要約つきブックマーク

Go + chiで作る、はてなブックマーク風の公開ブックマークサービスです。記事の要約を最大5つの日本語箇条書きで一覧に常時表示します。PC・モバイル対応。

- 新着・人気順、マイブックマーク、記事詳細と公開コメント
- URL保存・削除、コメント・タグ編集
- Google OAuth / OpenID Connectのみのログイン
- Gemini 3.8 Flashによる記事要約、関連ページ補足、進捗表示、手動再試行
- Cloud Run + Firestore、Secret Manager

## 必要なもの

- Go 1.26.8（`go.mod`のtoolchain指定で自動取得）
- Docker / Docker Compose（`docker-compose`コマンド。必要なら`docker compose`に読み替え）
- 本番構築にはGoogle Cloud CLI、課金を有効にしたGCPプロジェクト
- Google OAuthのWebクライアント、Gemini APIキー

## ローカル起動

```bash
cp .env.example .env
make emulator
make dev
```

http://localhost:8080 を開きます。認証情報なしでも空の公開一覧は表示できます。保存・編集・要約を使うには、`.env`のGoogle OAuth設定とGeminiキーを入力してください。認証を迂回する開発ログインはありません。

Google Cloud Consoleの「Google Auth Platform」で同意画面と**ウェブアプリケーション**のOAuthクライアントを設定し、以下を承認済みリダイレクトURIに登録します。

```text
http://localhost:8080/auth/google/callback
```

テスト公開中のOAuthアプリでは、ログインするGoogleアカウントをテストユーザーに追加します。スコープは`openid profile`のみ。Googleの表示名は公開ブックマーク・コメントに表示されます。

Gemini APIキーはGoogle AI Studioで取得し、`GEMINI_API_KEY`に設定します。モデルの既定値は`gemini-3.8-flash`です。本文を取得してGemini APIへ送信します。本文全体はFirestoreに保存しません。

`.env`はシェル形式です。特殊文字を含む値は単一引用符で囲み、信頼できる内容だけを記述してください。Git・Docker・Cloud Buildの送信対象から除外しています。

```bash
make help               # コマンド一覧
make fmt                # 整形
make test               # race検出つき単体テスト
make vet                # 静的解析
make build              # bin/server
make test-integration   # 起動済みEmulatorで統合テスト
make emulator-stop      # Emulator停止（データは永続化しません）
make preview            # localhost:8090、架空記事による画面確認のみ
```

`make test`ではEmulator依存のテストをスキップします。`make test-integration`は実際のFirestoreクライアントでトランザクション、二重保存、所有者チェック、要約リース、期限切れセッション等を検証します。テスト用プロジェクトは`demo-bookmark-test`です。Emulatorは本番の複合インデックス要件を完全には検証しません。

## Cloud Runへデプロイ

`.env`に実際の`PROJECT_ID`とGoogle OAuth/Geminiの値を設定します。`GOOGLE_CLOUD_PROJECT=demo-bookmark`はローカル用のままで構いません。本番の値はdeployスクリプトが設定します。

```bash
gcloud auth login
make bootstrap
make url
```

`make bootstrap`はAPI、Firestore Nativeの`(default)` DB、専用の実行・ビルドサービスアカウント、空のSecret、TTL、複合インデックスを作成します。初回実行者にはAPI有効化、DB・サービスアカウント・Secret作成、IAM設定の権限が必要です。リージョンの既定値は東京`asia-northeast1`です。既存DBは変更しません。DBのロケーションは作成後に変更できないため、`.env`の`REGION`を初期構築前に設定してください。

`make url`に表示される以下の形式のURIを、Google OAuthの承認済みリダイレクトURIに追加します。

```text
https://SERVICE-PROJECT_NUMBER.REGION.run.app/auth/google/callback
```

```bash
make secrets
make deploy
```

`make secrets`は`.env`の2つの秘密値をSecret Managerへ新しいバージョンとして登録します。`make deploy`はテスト・静的解析・ローカルビルド後、DockerfileをCloud Buildでビルドし、Chromiumを含むコンテナをCloud Runへ反映します。ソースデプロイするユーザーにはCloud Run Source Developer、Service Usage Consumer、実行・ビルドサービスアカウントへのService Account User等の権限が必要です。公開サービスのIAM設定には追加権限が必要になる場合があります。

Cloud Runは **1つのService** に限定し、HTTPアプリとFirestoreジョブワーカーを同じGoプロセスで動かします。既定は常時CPU割り当て（instance-based billing）、min 1 / max 3インスタンス、2 CPU、2 GiB、リクエスト300秒。min 1の待機時間も課金対象になるため、料金は [Cloud Run billing settings](https://docs.cloud.google.com/run/docs/configuring/billing-settings) を参照してください。公開一覧のためCloud Run自体は公開し、書き込みはアプリ内でGoogleログイン・CSRF検証を要求します。実行サービスアカウントにはFirestoreの`roles/datastore.user`と、このアプリの2つのSecretだけの読取権限を付与します。

インデックスの作成は非同期です。`gcloud firestore indexes composite list --project=PROJECT_ID`で全て`READY`になってから利用してください。独自ドメインを設定済みなら`.env`に`DEPLOY_BASE_URL=https://your-domain.example`を設定し、OAuth URIも合わせます。

FirestoreへはサーバーSDKとIAMでアクセスします。ブラウザー用Firebase SDKは使用しません。`firestore.rules`はブラウザーからのアクセスを全面拒否する参考設定です。既存のFirebaseプロジェクトへ導入する場合は既存ルールを確認し、意図せず公開しないでください。

## 構成

```text
cmd/server/          HTTPサーバー・起動と終了
internal/app/        chiルート、Google認証、CSRF、画面と要約の制御
internal/store/      Firestoreのトランザクション、セッション、生成制限
internal/content/    公開URLの安全な取得、本文抽出、Gemini API
internal/web/        Goに埋め込むテンプレート・CSS・JavaScript
scripts/             初期構築、Secret登録、インデックス、デプロイ
```

- 保存と最初の要約ジョブをFirestoreのトランザクションで同時に記録し、HTTP応答後は同じCloud Runサービスのワーカーがジョブを実行します。
- Firestoreの5分リースで複数インスタンス間の重複を抑え、各段階を記事へ記録します。処理が中断された場合は勝手に再開せず、Webから明示的に再試行します。
- Geminiの要約生成が失敗した場合は`failed`に保存します。ユーザーがWebで「要約を再試行」を押すまで再実行しません。
- 要約生成はユーザー単位で1分3件・UTC日付で1日50件まで。初回の自動キュー登録とWebからの明示的な再試行をFirestoreトランザクションで制限します。失敗した試行もカウントします。上限に達してもブックマークは保存され、後から要約を作成できます。
- 人気順は累計保存人数順です。期間別トレンドランキングではありません。
- 一覧は20件ずつ、詳細の公開ブックマークは新しい順に最大50件です。
- 要約の編集方針は`internal/content/prompt.go`の`SummaryInstruction()`で変更できます。構造化出力とサーバー側検証で1〜5項目を必須にします。
- 記事取得は公開HTTP/HTTPSの標準ポートのみ。DNS解決後のIPへ接続し、内部IP・リダイレクト・サイズ・時間を検証します。
- 本文が短い、またはGeminiが内容不足と判定した場合は、安全なHTTPクライアントで取得したHTMLをChromiumで描画し、動的表示の本文を抽出します。ページ内リンクは元ページから直接リンクされた公開HTMLを最大3件まで取得します。子ページから先へは進みません。
- Chromiumには取得済みHTMLを渡し、ブラウザーの外部通信を遮断します。関連ページURLもSSRF対策つきHTTPクライアントで再検証します。
- JavaScript外部配信に依存するSPA、ログインが必要な記事、取得を拒否するサイト、PDFには対応しません。取得に失敗してもブックマークは保持します。
- 要約は保存後に生成されるため、取得前のタイトルにはドメイン名を表示します。AI出力はHTMLとして実行しません。

## 確認する項目

実環境の資格情報を設定後、Googleログイン→URL保存→要約→編集→削除→ログアウトを確認してください。OAuth同意画面の公開設定、モデル利用可否、請求・クォータは利用プロジェクトに依存します。秘密値をIssueやチャットへ貼り付ける必要はありません。

## 実装確認

初期実装では署名付きテストIDトークンとFirestore EmulatorによるHTTPフロー、同時保存、生成制限、PC 1365pxとモバイル390px・320pxの画面を確認しました。今回追加した非同期ジョブ・Chromium補足取得については、コード整形と`go build`を確認しています。今回の変更後にテストスイートは実行していません。

実際のGoogleログイン・Gemini API呼び出し・Cloud Build/Cloud Runデプロイは利用者のプロジェクトと資格情報が必要なため未実施です。

参考: [Gemini 3.8 Flash](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash)、[構造化出力](https://ai.google.dev/gemini-api/docs/structured-output)、[Cloud Runのソースデプロイ](https://cloud.google.com/run/docs/deploying-source-code)。
