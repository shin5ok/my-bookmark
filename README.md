# しおり — AI要約つきブックマーク

Go + chiで作る、IAPで保護されたブックマークサービスです。記事の要約を最大5つの日本語箇条書きで一覧に常時表示します。PC・モバイル対応。

- 新着・星の合計による人気順、マイブックマーク、未理解だけを表示する「まだ」、記事詳細と公開コメント
- 自分のブックマークを5段階の星で評価・変更・解除
- URL保存・削除、コメント・タグ編集
- 本番はCloud Run IAP、ローカル開発はGoogle OAuth / OpenID Connectでログイン
- Gemini 3.8 Flashによる記事・YouTube動画の要約、関連ページ補足、進捗表示、手動再試行
- Cloud Tasksと専用Cloud Runワーカーによる非同期要約
- Cloud Run + Firestore、Identity-Aware Proxy

## 理解状態と星評価

マイブックマークのサブメニュー「まだ」は、自分がまだ理解済みにしていない記事だけを新着順で表示します。理解状態が未保存の古いブックマークも対象です。「理解した」を押すと「まだ」から外れます。通常の一覧ではタイトルだけに折りたたまれ、タイトルを押すと展開し、「✓ 理解済み」を押すと解除できます。

保存したブックマークは星1〜5個で評価でき、「解除」で未評価（0）に戻せます。星はユーザーごとに保存され、人気一覧は全ユーザーの星の合計が多い順です。同点は記事IDの降順で安定させます。評価の変更・解除・ブックマーク削除は合計にも反映されます。カードの「⭐ 数値」は記事全体の星の合計、「あなたの評価」は自分の星です。

既存環境への初回導入は、デプロイ前に次を実行してください。

```bash
make indexes           # active + rating_total のインデックスを作成
make migrate-ratings   # 既存記事の rating_total を0で初期化
make deploy
```

Firestoreのインデックスが利用可能になってからデプロイしてください。`make migrate-ratings`は`.env`の`PROJECT_ID`とADCを使い、ローカルEmulatorの設定を外して実行します。既存の星合計は変更せず、再実行できます。初期化前の古い記事は、Firestoreの並べ替え対象に含まれないため人気一覧に表示されません。「まだ」は保存日時順にページを読み進めて絞り込み、1ページ最大20件を表示するため、理解済み記事が多いと追加の読み取りが発生します。

## YouTube動画と非同期要約

公開YouTube動画の `watch?v=...`、`youtu.be/...`、`shorts/...`、`live/...`、`embed/...` を保存すると、映像・音声から日本語タイトル・TL;DR・要約を生成します。時刻指定や共有用パラメータは動画入力から除き、動画全体を要約します。チャンネルや再生リストには対応していません。視聴制限やモデル側の制約で読めない場合は失敗理由を表示します。

本番の処理は「保存 → Firestoreの待機ジョブ → Cloud Tasks → 専用Cloud Runワーカー → Firestoreへ結果保存」です。ブラウザとトークンAPIは保存後すぐに応答し、画面は既存の進捗取得で結果を更新します。画面用サービスの常駐処理は2秒ごとに待機ジョブをCloud Tasksへ送信します。この送信処理のため、画面用サービスは引き続き `--min=1 --no-cpu-throttling` が必要です。タスク登録に失敗したジョブはFirestoreに残り、次回の送信対象になります。

タスク名は記事IDと依頼時刻から決定し、登録の再試行を重複排除します。ワーカーはFirestoreトランザクションで11分の実行権を取得し、古い依頼・完了済み・キャンセル済みの配信を無視します。処理は9分、動画へのGemini呼び出しは8分、Cloud TasksのHTTP期限は10分、Cloud Runの期限は11分です。429・5xx・通信障害は最大3回の要約実行まで再試行し、その後は失敗を表示します。保存障害や稼働中の重複配信は非2xxで返し、Cloud Tasksが再配信します。プロセス停止時は実行権の期限切れ後に再開できます。

Cloud Tasksは同時実行3件、毎秒1件、再試行間隔30〜600秒です。インフラ障害での再配信に回数上限は設けません（Cloud Tasks自体の保持期限は適用されます）。専用ワーカーは `WORKER_ONLY=true`、最小0インスタンスで稼働し、Cloud Run IAMが `${SERVICE}-tasks` サービスアカウントのOIDC認証を検証します。タスク用エンドポイントを画面用サービスや公開トークンAPIに追加しません。

既存環境への導入は `make bootstrap` → `make deploy` の順です。bootstrapはCloud Tasks API、タスク呼び出し用サービスアカウント、実行用アカウントの `roles/cloudtasks.enqueuer` と呼び出し用アカウントに対する `roles/iam.serviceAccountUser` を設定します。deployはキューと専用ワーカー、ワーカーへの `roles/run.invoker` を設定してから画面用サービスを更新します。既存のFirestoreインデックスを利用します。ローカル開発は `TASKS_*` を空欄にして従来の直接実行を使えます。

動画の入力形式は[Google公式サンプル](https://cloud.google.com/vertex-ai/generative-ai/docs/samples/googlegenaisdk-textgen-with-youtube-video)、非同期配信は[Cloud TasksのHTTPタスク](https://cloud.google.com/tasks/docs/creating-http-target-tasks)に準拠しています。

## 必要なもの

- Go 1.26.8（`go.mod`のtoolchain指定で自動取得）
- Docker / Docker Compose（`docker-compose`コマンド。必要なら`docker compose`に読み替え）
- 本番構築にはGoogle Cloud CLI、`jq`、課金を有効にしたGCPプロジェクト
- ローカル用Google OAuthのWebクライアント、Gemini用のApplication Default Credentials（ADC）

## ローカル起動

```bash
cp .env.example .env
make emulator
make dev
```

ローカルでは先に `gcloud auth application-default login` を実行します。http://localhost:8080 を開きます。保存・編集には`.env`のGoogle OAuth設定、要約にはADCが必要です。認証を迂回する開発ログインはありません。

Google Cloud Consoleの「Google Auth Platform」で同意画面と**ウェブアプリケーション**のOAuthクライアントを設定し、以下を承認済みリダイレクトURIに登録します。

```text
http://localhost:8080/auth/google/callback
```

テスト公開中のOAuthアプリでは、ログインするGoogleアカウントをテストユーザーに追加します。スコープは`openid profile`のみ。Googleの表示名は公開ブックマーク・コメントに表示されます。

GeminiはVertex AI経由で呼び出し、APIキーは使用しません。ADCのプロジェクトにはVertex AI APIとモデル利用権限が必要です。モデルの既定値は`gemini-3.8-flash`、ロケーションは`global`で、`GEMINI_MODEL`と`GEMINI_LOCATION`で変更できます。本文全体はFirestoreに保存しません。

`.env`はシェル形式です。特殊文字を含む値は単一引用符で囲み、信頼できる内容だけを記述してください。Git・Docker・Cloud Buildの送信対象から除外しています。

```bash
make help               # コマンド一覧
make apis               # 必要なGoogle Cloud APIを有効化
make migrate-ratings    # 既存記事の星合計を初期化（デプロイ先・ADC）
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

`.env`の`PROJECT_ID`と`GOOGLE_CLOUD_PROJECT`を、Vertex AIを有効化する実際の同一GCPプロジェクトに設定します。本番の`GOOGLE_CLOUD_PROJECT`はdeployスクリプトが`PROJECT_ID`から設定します。

本番でログインを許可するアカウントをルートの`allow_accounts.yaml`に記入します。`accounts`には個別のメールアドレス、`domains`にはGoogle Workspace / Cloud Identityドメインを書きます。実際の許可対象はこのファイルの内容で決まります。ファイルが空または不正ならデプロイまたはサーバー起動が失敗します。

```yaml
accounts:
  - reader@example.com
domains:
  - data-cloud.jp
```

```bash
gcloud auth login
make bootstrap
make deploy
```

`make apis`は`.env`の`PROJECT_ID`を対象に、Cloud Run・Firestore・Cloud Build・Artifact Registry・Secret Manager・IAM・Vertex AI・IAP・Cloud TasksのAPIを有効化します。有効済みのAPIはスキップします。`make bootstrap`と`make deploy`からも自動実行します。

`make bootstrap`はAPI（IAPを含む）、Firestore Nativeの`(default)` DB、専用の実行・ビルドサービスアカウント、TTL、複合インデックスを作成し、実行サービスアカウントへFirestoreとVertex AIの権限を付与します。リージョンの既定値は東京`asia-northeast1`です。既存DBは変更しません。DBのロケーションは作成後に変更できません。

`make deploy`はテスト・静的解析後にイメージを構築し、Cloud Runを`--iap --no-allow-unauthenticated`で更新します。IAPサービスエージェントへCloud Run Invokerを付与し、`allow_accounts.yaml`からサービス単位のIAPアクセス権を同期します。既存の同ロールの許可は置き換え、他のロールは保持します。アプリもIAP署名付きJWTを毎回検証し、許可リストと照合します。デプロイ実行者にはCloud Build、Cloud Run、IAPポリシーを更新する権限が必要です。

Cloud Runの画面用Serviceでは、HTTPアプリとFirestoreジョブワーカーを同じGoプロセスで動かします。既定は常時CPU割り当て（instance-based billing）、min 1 / max 3インスタンス、1 CPU、2 GiB、リクエスト300秒。API専用Serviceも1 CPUです。min 1の待機時間も課金対象になるため、料金は [Cloud Run billing settings](https://docs.cloud.google.com/run/docs/configuring/billing-settings) を参照してください。実行サービスアカウントには`roles/datastore.user`と`roles/aiplatform.user`を付与します。

IAPのGoogle管理OAuthクライアントは通常、同一組織内のユーザー向けです。プロジェクトが`data-cloud.jp`と別組織にある場合、[Cloud Run IAPの外部ユーザー設定](https://docs.cloud.google.com/run/docs/securing/identity-aware-proxy-cloud-run)に従いカスタムOAuthクライアントを構成してください。IAMの`domain:`許可はWorkspaceの顧客IDで評価されるため、セカンダリドメインにも及び得ます。アプリはJWTのメールアドレスが`@data-cloud.jp`に一致するかを別途検査します。

インデックスの作成は非同期です。`gcloud firestore indexes composite list --project=PROJECT_ID`で全て`READY`になってから利用してください。独自ドメインを設定済みなら`.env`に`DEPLOY_BASE_URL=https://your-domain.example`を設定します。

FirestoreへはサーバーSDKとIAMでアクセスします。ブラウザー用Firebase SDKは使用しません。`firestore.rules`はブラウザーからのアクセスを全面拒否する参考設定です。既存のFirebaseプロジェクトへ導入する場合は既存ルールを確認し、意図せず公開しないでください。

## 構成

```text
cmd/server/          HTTPサーバー・起動と終了
internal/app/        chiルート、IAP/ローカルGoogle認証、CSRF、画面と要約の制御
internal/iap/        許可リスト、IAP署名検証、ポリシー同期
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

実環境で許可アカウントのアクセス、許可外アカウントの拒否、URL保存→要約→編集→削除を確認してください。IAPの設定、モデル利用可否、請求・クォータは利用プロジェクトに依存します。IAPログアウト後もGoogle側のセッションが有効なら自動で再ログインする場合があります。

## 実装確認

初期実装では署名付きテストIDトークンとFirestore EmulatorによるHTTPフロー、同時保存、生成制限、PC 1365pxとモバイル390px・320pxの画面を確認しました。今回追加した非同期ジョブ・Chromium補足取得については、コード整形と`go build`を確認しています。今回の変更後にテストスイートは実行していません。

実際のGoogleログイン・Gemini API呼び出し・Cloud Build/Cloud Runデプロイは利用者のプロジェクトと資格情報が必要なため未実施です。

参考: [Gemini 3.8 Flash](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash)、[構造化出力](https://ai.google.dev/gemini-api/docs/structured-output)、[Cloud Runのソースデプロイ](https://cloud.google.com/run/docs/deploying-source-code)。

## 専用トークンでURLを登録するAPI

ログイン後、サイドバーの **APIトークン** を開き「トークンを発行」を押します。
トークンは発行直後に一度だけ表示され、90日間有効です。1ユーザーにつき1つで、
再発行すると旧トークンが無効になります。「トークンを失効」でも停止できます。
Firestoreの `api_tokens` にはトークンのSHA-256ハッシュ・所有者・メールアドレス・有効期限を保存し、平文トークンは保存しません。

画面に表示されるAPIエンドポイントに、クエリパラメータ `token` を付けて送信します。
`SHIORI_TOKEN` は発行したトークン、`SHIORI_API_URL` は画面のAPIエンドポイントです。

```bash
curl --request POST "${SHIORI_API_URL}?token=${SHIORI_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data '{"url":"https://example.com/article","comment":"あとで読む","tags":["技術","Go"]}'
```

`--request PUT` でも同じ形式を使えます。URLだけなら `{"url":"https://example.com/article"}` で登録できます。
`url` フィールドに説明文やMarkdownリンクを含めても、HTTP/HTTPSのURLが1件なら抽出して登録します。
例：`{"url":"あとで読む: [記事](https://example.com/article)。"}`。
URLが見つからない場合や2件以上ある場合は400を返します。抽出したURLにも通常の公開URL検証を適用します。
どちらも正規化したURLをキーに、トークン所有者のブックマークを登録・更新します。
同じURLの再送で登録件数は増えません。コメント・タグは置き換え、省略した場合は空になります。
新しい記事は既存の要約キューに入り、要約は非同期で作成されます（既存の利用上限も適用）。
GETでは登録できません。CookieではAPI認証できません。

成功時はHTTP 200のJSONです。

```json
{"id":"記事ID","url":"https://example.com/article","title":"example.com","status":"queued","article_url":"https://画面のホスト/articles/記事ID"}
```

エラーは `{"error":"説明"}` を返します。400は入力不正、401は未指定・無効・期限切れトークン、
403は許可されていないアカウント、415はJSON以外、429は利用上限、503は保存先の一時エラーです。
コメントは500文字、タグは5つまで・各24文字、リクエスト本文は16KiBまでです。

### 本番デプロイ

`make deploy` は同じイメージから2つのCloud Runサービスを構築します。

- `${SERVICE}`：IAPで保護した画面。トークンの発行・失効はログインとCSRF検証が必要です。
- `${SERVICE}-api`：`API_ONLY=true` のAPI専用サービス。Cloud Runの入口は公開し、アプリで専用トークンを検証します。画面・OAuth・トークン発行ルートは公開しません。要約ワーカーは画面側で動きます。

API側でも `allow_accounts.yaml` のメールアドレス／ドメインを毎リクエスト照合します。
許可リストの変更は両サービスへ再デプロイしてください。APIのURLは画面サービスの `API_BASE_URL` に自動設定されます。
追加のサービスが作られるため、デプロイにはCloud Runの公開設定とCloud Loggingのsink更新権限が必要です。

URLに認証情報が含まれるため、デプロイスクリプトはAPI公開前に `_Default` sinkへ
APIサービスのCloud Runリクエストログ除外を追加します（再デプロイ時は更新）。
独自sink・組織の集約sink・外部プロキシがある場合は、それらにも同様の除外／クエリ秘匿設定が必要です。
アプリのレスポンスは `Cache-Control: no-store`、トークン画面は `Referrer-Policy: same-origin`、APIは `Referrer-Policy: no-referrer` を返します。
トークン画面では同一サイトのフォーム送信に必要なOriginを保持し、外部へのReferer送信を抑止します。
トークン付きURLをブラウザのアドレス欄・共有メッセージに貼り付けず、クライアント側でもURLをログに残さないでください。

ローカル開発では通常のサービスが `/api/bookmarks` も提供し、API用の別プロセスは不要です。

### APIが401を返す場合

認証エラーは `error` に加え、`code` と日本語の `message` を返します。

- `token_missing`：URLの `token` パラメータがない、または空です。
- `token_ambiguous`：`token` パラメータが複数あります。
- `token_malformed`：コピーした値が所定の形式ではありません。トークンは108文字（64文字のID、ドット、43文字の秘密値）です。
- `token_invalid`：現在のトークンと一致しません。発行元のAPIエンドポイントと最新のトークンを確認してください。再発行・失効すると旧トークンは使えません。
- `token_expired`：一致したトークンの有効期限が切れています。画面から再発行してください。

再発行後はcurlに指定する値も置き換えてください。エラーを共有する際はトークン本体を伏せ、`code` と `message` のみ共有してください。

シェル変数でトークンを渡す場合、URLはダブルクォートで囲んでください。
シングルクォート内の `$SHIORI_API_TOKEN` は展開されず、変数名がそのまま送信されます。

```bash
curl -X POST "https://APIホスト/api/bookmarks?token=${SHIORI_API_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/article"}'
```

発行直後の画面にはトークン全体と、発行した値を埋め込んだcurlをコピーするボタンがあります。
APIはコピー時に付いた前後の空白・改行を除去してから検証します（内部の文字は変更しません）。

初回のURL登録時も、要約完了時にAIが生成した簡潔な日本語タイトルへ更新します。生成タイトルが得られない場合は、取得元の記事タイトルを使用します。

## TL;DRと具体的な要約

新規登録・標準の再試行は「具体的に」と同じ指示で要約します。
簡潔な日本語タイトルの下に、結論・重要性・影響を伝えるTL;DRを原則3つの短文で表示します。
内容に応じて2〜5文とし、各文は100文字以内。スマホでの折り返しは許容し、文章は途中で切りません。
一覧ではTL;DRを短い箇条書きで表示します。「要約を読む」を押すと、そのエントリー内で要約本文を開閉できます。画面遷移はありません。
本文では数値・事例・手順・注意点を確認できます。

再生成ではタイトル・TL;DR・要約本文をまとめて更新し、失敗した場合は以前の内容を保持します。
TL;DRのない既存記事も要約本文は折りたたんで表示し、新規登録または手動再生成から適用します。
既存記事の一括再生成は行いません。
