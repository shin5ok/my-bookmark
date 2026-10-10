# しおり — AI要約つきブックマーク

Go + chiで作る、IAPで保護されたブックマークサービスです。一覧では日本語タイトルと短いTL;DRで要点を確認し、必要に応じて最大5項目の要約本文を開けます。PC・モバイル対応。

- 新着・星の合計による人気順、マイブックマーク、未理解だけを表示する「まだ」、記事詳細と公開コメント
- 自分のブックマークを5段階の星で評価・変更・解除
- URL保存・削除、コメント・タグ編集
- 本番はCloud Run IAP、ローカル開発はGoogle OAuth / OpenID Connectでログイン
- Geminiによる記事・重要な画像・YouTube動画の要約、関連ページ補足、進捗表示、手動再試行
- Cloud Tasksと専用Cloud Runワーカーによる非同期要約
- Cloud Run + Firestore、Identity-Aware Proxy

### 目次

- [使い方：メニュー・理解状態・星評価](#使い方)
- [記事・画像・YouTubeの要約](#記事画像youtubeの要約)
- [Cloud Tasksによる非同期処理](#cloud-tasksによる非同期処理)
- [ローカル起動とテスト](#ローカル起動)
- [Cloud Runへのデプロイと既存環境の更新](#cloud-runへデプロイ)
- [専用トークンでURLを登録するAPI](#専用トークンでurlを登録するapi)
- [TL;DRと具体的な要約](#tldrと具体的な要約)

## 使い方

### メニュー

`/` はマイブックマークのページです。ログイン済みなら自分のブックマークを直接表示し、未ログインならログイン画面へ進みます。既存の `/mine` も同じ一覧を表示します。新着は `/new`、人気は `/popular` です。旧URLの `/?sort=new`・`/?sort=popular` は対応する新しいパスへ転送し、ページ送りの指定も引き継ぎます。

起動時から、メニューの一番上に「マイブックマーク」とサブメニュー「まだ」を表示します。PCではサイドバーの先頭、スマートフォンでは「新着」「人気」より上の行に表示します。

| メニュー | 表示する内容 |
| --- | --- |
| マイブックマーク（`/`） | 自分が保存した記事を、保存日時の新しい順に表示 |
| まだ（`/?filter=unread`） | 自分のブックマークのうち、理解済みにしていない記事だけを表示 |
| 新着（`/new`） | 保存された記事を新しい順に表示 |
| 人気（`/popular`） | 全ユーザーの星の合計が多い順に表示 |

「まだ」は未理解の状態を示します。記事を開いたかどうかによる既読・未読の判定ではありません。理解状態が未保存の古いブックマークも対象です。

### 理解した記事を折りたたむ

1. 自分のブックマークで「理解した」を押すと、理解済みとして保存し、カードをタイトルだけに折りたたみます。
2. 折りたたまれたタイトルを押すと、カードを再び展開します。タイトルを押すだけでは理解状態は変わりません。もう一度タイトルを押すと折りたためます。
3. 展開したカードの「✓ 理解済み」を押すと、理解済みを解除し、通常の表示に戻します。「まだ」の対象にも戻ります。

理解状態はユーザーごとのブックマークに保存します。他のユーザーには影響しません。理解済みの記事は再読み込み後もタイトルだけに折りたたみます。「まだ」で「理解した」を押した場合は一覧を更新し、その記事を表示対象から外します。ボタンの表記は「理解した！」から「理解した」に変更しています。

### 星で評価する

自分が保存したブックマークには、5個の星から1〜5段階の評価を付けられます。例えば4番目の星を押すと `⭐️⭐️⭐️⭐️☆`、5番目なら `⭐️⭐️⭐️⭐️⭐️` になります。別の星を押すと評価を変更でき、「解除」で未評価（0）に戻せます。

- 「あなたの評価」は、自分が付けた星の数です。
- カードの「⭐ 数値」は、その記事に対する全ユーザーの星の合計です。
- 「人気」は星の合計が多い順です。平均評価ではありません。同点は記事IDの降順で表示順を安定させます。
- 評価の変更・解除・ブックマークの削除は、星の合計にも反映します。評価は1人1記事につき1つです。

「まだ」の一覧は保存日時順に読み進めて未理解の記事を集め、1ページ最大20件を表示します。理解済みの記事が多い場合は、通常のマイブックマーク一覧よりFirestoreの読み取りが増えます。

## 記事・画像・YouTubeの要約

URLを保存すると、要約を非同期で作成します。完了を待たずに保存が終わり、画面には進捗が表示されます。完了すると、簡潔な日本語タイトル・TL;DR・最大5項目の要約に更新します。要約の詳しさと再生成については「[TL;DRと具体的な要約](#tldrと具体的な要約)」を参照してください。

### 記事の本文と関連ページ

公開HTMLページの本文を取得して要約します。本文が短い、またはGeminiが内容不足と判断した場合は、取得済みHTMLをChromiumで描画して本文を補います。それでも補足が必要な処理では、元のページから直接リンクされた関連HTMLページを最大3件まで取得します。関連ページからさらにリンクをたどることはありません。

### 重要な画像も要約に使う

記事本文内の図表・グラフ・構成図・手順を説明する画像を候補にします。Geminiが画像の説明文・キャプション・周囲の文章から要約に役立つものを選び、選ばれた画像データを本文と一緒に分析します。

| 項目 | 動作・制限 |
| --- | --- |
| 選ぶ枚数 | 通常は最も重要な1枚。別の重要情報を補う場合だけ最大3枚。役立つ画像がなければ0枚 |
| 取得の上限 | 1回の要約実行全体で最大3枚の取得を試みる。ページを描画し直しても上限を引き継ぐ |
| 候補の範囲 | 元の記事の本文部分。関連ページの画像は取得しない |
| 除外する候補 | ロゴ、広告、小さなアイコン、ナビゲーション内の画像など |
| HTMLの対応 | 通常の画像URL、遅延読み込み、`srcset`・`picture` の画像候補 |
| 取得する形式 | JPEG・PNG・WebP。SVG・GIF・埋め込みのdata URLは対象外 |
| 容量・時間 | 1枚4MB以下、画像の取得は1枚12秒以内 |
| JPEG・PNGの検査 | 縦横とも80px以上、合計4,000万画素以下。画像ヘッダーが読めない場合もスキップ |
| 安全性 | 本文と同じSSRF対策付きHTTPクライアントを使用。内部アドレスへのアクセスやリダイレクトを拒否 |
| 保存する内容 | 要約と、取得した画像のURLを参照元に保存。画像データそのものはFirestoreに保存しない |

図表の数値・比較・手順を本文と関連づけて要約します。軸・単位・期間・凡例を確認し、判読できない文字や数値は推測しないよう指示します。画像が装飾的・無関係・不鮮明なら要約には使いません。

画像の選別・取得に失敗した場合は、その画像をスキップします。一部だけ取得できた場合は取得できた画像を使い、すべて取得できない場合は本文だけで要約を続けます。最終的な要約生成自体が失敗した場合は、通常の要約エラーとして扱います。

画像候補がある記事では、画像を選ぶGemini呼び出しが追加されます。画像を添付する場合は、その入力分のモデル利用も増えます。選別の期限は30秒、画像付き要約の期限は90秒です。入力形式は[Vertex AIの画像理解](https://docs.cloud.google.com/gemini-enterprise-agent-platform/models/capabilities/image-understanding)に準拠しています。

既存記事の要約は自動で一括更新しません。画像を反映した要約を作り直したい場合は、画面から再生成してください。

### YouTube動画

公開YouTube動画を保存すると、映像・音声から日本語タイトル・TL;DR・要約を生成します。

- 対応するURLは `watch?v=...`、`youtu.be/...`、`shorts/...`、`live/...`、`embed/...` です。
- 動画IDから通常の視聴URLにそろえ、時刻指定・共有用パラメータは動画入力から除きます。要約する対象は動画全体です。
- チャンネル・再生リストには対応していません。
- 視聴制限やモデル側の制約で動画を読めない場合は、失敗理由を表示します。

動画はGeminiに映像・音声を直接渡す処理です。HTML記事向けの「重要な画像を最大3枚選ぶ」処理とは別に実行します。動画入力は[Google公式サンプル](https://cloud.google.com/vertex-ai/generative-ai/docs/samples/googlegenaisdk-textgen-with-youtube-video)に準拠しています。

## Cloud Tasksによる非同期処理

本番では、次の順序で要約を作成します。

```text
URLを保存
  → Firestoreにブックマークと待機ジョブを保存
  → 画面サービスがCloud Tasksへ登録
  → 専用のCloud Runワーカーが本文・画像・動画を要約
  → Firestoreに結果を保存
  → 画面が進捗APIから結果を取得して表示
```

ブラウザとトークンAPIは保存後すぐに応答します。画面サービスは2秒ごとにFirestoreの待機ジョブをCloud Tasksへ送ります。タスク登録が失敗してもジョブはFirestoreに残り、次回の送信対象になります。この常駐処理のため、画面サービスには `--min=1 --no-cpu-throttling` が必要です。

### 重複・中断・再試行

- タスク名は記事IDと依頼時刻から決め、同じ依頼のタスク登録を重複させません。
- ワーカーはFirestoreのトランザクションで実行権を取得します。実行権の有効期間は11分で、進捗更新時に延長します。
- 古い依頼・完了済み・キャンセル済みの配信は処理しません。実行中の重複配信は再配信の対象にします。
- 最終的な要約生成の429・5xx・通信障害は、最大3回の要約実行まで再試行します。その後は失敗を表示し、画面から再試行できます。画像の選別・取得の失敗は本文での要約に切り替えます。
- 保存障害は再配信の対象です。プロセスが停止した場合は、実行権の期限が切れると再開できます。

| 設定 | 値 |
| --- | --- |
| ワーカー内の要約処理全体 | 最大9分 |
| YouTube向けGemini呼び出し | 最大8分 |
| Cloud TasksのHTTP期限 | 10分 |
| Cloud Runワーカーのリクエスト期限 | 11分 |
| キューの同時配信 | 最大3件 |
| キューの配信速度 | 毎秒1件 |
| 再試行の待ち時間 | 30〜600秒 |

インフラ障害によるタスク配信の再試行回数には上限を設定していません。Cloud Tasks自体の保持期限は適用されます。配信設定は[Cloud TasksのHTTPタスク](https://cloud.google.com/tasks/docs/creating-http-target-tasks)に準拠しています。

### ローカルと本番の設定

`make deploy` が本番のタスク関連設定を自動で設定します。通常は手作業で入力する必要はありません。

| 環境変数 | 用途 |
| --- | --- |
| `TASKS_QUEUE` | `projects/…/locations/…/queues/…` 形式のキュー名 |
| `TASKS_WORKER_URL` | 専用ワーカーのCloud Run URL |
| `TASKS_SERVICE_ACCOUNT` | タスク呼び出し時のOIDC認証に使うサービスアカウント |
| `WORKER_ONLY=true` | タスク処理専用で起動。通常の画面や公開トークンAPIは提供しない |
| `API_ONLY=true` | トークンAPI専用で起動。要約ワーカーは実行しない |

ローカル開発では `WORKER_ONLY=false`、`API_ONLY=false` とし、`TASKS_QUEUE`・`TASKS_WORKER_URL`・`TASKS_SERVICE_ACCOUNT` を空欄にします。画面と同じプロセスがFirestoreの待機ジョブを直接処理するため、ローカルでCloud Tasksを構築する必要はありません。

### Chromiumのsandbox

記事の描画に使うChromiumはsandboxを有効にして起動します。`no-sandbox=false` を明示し、chromedpがroot実行時にsandboxを自動無効化する動作も防ぎます。描画を行うプロセスは非rootで実行してください。コンテナは `USER 65532:65532` を使用します。

専用ワーカーとローカルの直接実行ワーカーは、DB接続・ジョブ処理・HTTP待ち受けの前に、外部通信のないHTMLでChromium起動・JavaScript実行・本文抽出を確認します。root実行、ブラウザー未検出、sandbox非対応などで起動チェックに失敗した場合はプロセスを停止します。sandboxを無効化して再起動するフォールバックはありません。API専用サービスとCloud Tasksのディスパッチャーは描画しないため、このチェックを行いません。

`make deploy` は専用ワーカーに `--execution-environment=gen2` を指定します。Linuxのnamespace sandbox・seccompが利用できる実行環境が必要であり、gen2指定だけで動作を保証するものではありません。Cloud Runはsetuid helperによる権限昇格をサポートしません。

ローカルでブラウザーを自動検出できない場合は、`.env` に `CHROME_BIN=/実際の/ChromeまたはChromiumのパス` を設定してください。起動チェックには通常の描画タイムアウト（15秒）を適用し、約1.2秒の描画待機があります。

テストではsandbox無効化引数が渡されないこと、起動失敗時に再起動しないこと、チェック対象のサービス分岐、起動失敗時のサーバー停止、インストール済みChromeでの描画を確認します。ローカルのmacOSテストはLinuxのnamespace・seccompの有効状態を証明しません。本番反映前に同じイメージを使ったCloud Runの検証用ワーカーで起動と描画、およびrendererのsandbox状態を確認してください。コンテナ側のseccomp設定だけをChromiumのsandboxの証拠として扱わないでください。

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
make preview            # localhost:8090/new、架空記事の画面確認
```

`make test`ではEmulator依存のテストをスキップします。`make test-integration`は実際のFirestoreクライアントでトランザクション、二重保存、所有者チェック、要約リース、期限切れセッション等を検証します。テスト用プロジェクトは`demo-bookmark-test`です。Emulatorは本番の複合インデックス要件を完全には検証しません。

### 画像要約の機能テスト

`internal/app/image_summary_functional_test.go` の `TestImageSummaryFunctionalFlow` は、`make test-integration` に含まれます。次の一連の処理を、実際のHTTPハンドラー・Geminiクライアント・Firestore Emulatorで検証します。

```text
ブックマーク保存 → 待機ジョブ → タスク配信 → 画像付きGeminiリクエスト
  → 要約・TL;DR・参照元の保存 → 進捗APIとマイブックマーク一覧の表示
```

| ケース | 確認する動作 |
| --- | --- |
| 通常の1枚・最大3枚・重要画像なし | 選ばれた枚数・順序・画像データを送信し、結果と参照元を保存する |
| 本文が短い記事 | 説明画像があれば画像付きで要約する |
| 画像の取得失敗 | 全失敗なら本文で継続。一部失敗なら取得できた画像を使う |
| 画像選別APIの429 | 画像なしで本文の要約を継続する |
| 不正な選別結果 | 4枚選択・存在しない番号・重複番号を拒否する |
| 内部アドレスへのリダイレクト | 内部アドレスへリクエストを送らず、本文の要約を継続する |
| 完了したタスクの再配信 | 画像取得・Gemini呼び出しを繰り返さない |

合計11ケースです。記事・画像・モデル応答にはローカルHTTPフィクスチャを使うため、実サイト・Vertex AI・本番認証情報には依存しません。AIの選別・画像読解の精度や、Cloud TasksとCloud Runの実環境のIAM設定は、このテストでは検証しません。

この機能テストだけを実行する場合も、先に `make emulator` でFirestore Emulatorを起動してください。

```bash
FIRESTORE_EMULATOR_HOST=127.0.0.1:8085 go test -race ./internal/app \
  -run '^TestImageSummaryFunctionalFlow$' -count=1 -v
```

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
# FirestoreインデックスがREADYになったことを確認してから実行
make deploy
```

### 必要なAPIを有効化する

`.env` の `PROJECT_ID` を対象に、必要なAPIをまとめて有効化できます。

```bash
make apis
```

対象はCloud Run・Firestore・Cloud Build・Artifact Registry・Secret Manager・IAM・Vertex AI・IAP・Cloud Tasksの9種類です。有効済みのAPIはスキップします。`make bootstrap` と `make deploy` からも自動で実行するため、通常は個別に有効化する必要はありません。`make apis` はAPIの有効化だけを行い、DB・キュー・サービスアカウントの構築は `make bootstrap` と `make deploy` が行います。

### 構築・デプロイで行うこと

`make bootstrap`はAPI（IAPを含む）、Firestore Nativeの`(default)` DB、専用の実行・ビルド・タスク呼び出し用サービスアカウント、TTL、複合インデックスを作成し、実行サービスアカウントへFirestore・Vertex AI・Cloud Tasksの権限を付与します。リージョンの既定値は東京`asia-northeast1`です。既存DBは変更しません。DBのロケーションは作成後に変更できません。

`make deploy`はテスト・静的解析後にイメージを構築し、画面サービスを`--iap --no-allow-unauthenticated`で更新します。IAPサービスエージェントへCloud Run Invokerを付与し、`allow_accounts.yaml`からサービス単位のIAPアクセス権を同期します。既存の同ロールの許可は置き換え、他のロールは保持します。アプリもIAP署名付きJWTを毎回検証し、許可リストと照合します。デプロイ実行者にはCloud Build、Cloud Run、IAPポリシーを更新する権限が必要です。

同じイメージから、役割ごとに3つのCloud Runサービスを作成します。

| サービス | 役割と入口 | 既定のリソース |
| --- | --- | --- |
| `${SERVICE}` | IAPで保護した画面・トークン設定。待機ジョブをCloud Tasksへ送信 | 1 CPU、2 GiB、最小1 / 最大3、期限300秒、常時CPU割り当て |
| `${SERVICE}-api` | 専用トークンでURLを登録する公開API | 1 CPU、512 MiB、最小0 / 最大3、期限60秒 |
| `${SERVICE}-worker` | IAMで保護した要約処理。タスク用サービスアカウントが呼び出す | 1 CPU、2 GiB、最小0 / 最大3、同時リクエスト1、期限660秒 |

画面サービスは要約そのものを実行せず、タスク登録を行います。要約は専用ワーカーが実行します。画面サービスの最小1インスタンスは待機中も課金対象です。料金設定は[Cloud Run billing settings](https://docs.cloud.google.com/run/docs/configuring/billing-settings)を参照してください。

実行サービスアカウントには `roles/datastore.user`・`roles/aiplatform.user`・`roles/cloudtasks.enqueuer` を付与します。タスク用の `${SERVICE}-tasks` アカウントに対する `roles/iam.serviceAccountUser` を実行用アカウントに付与し、ワーカーにはタスク用アカウントの `roles/run.invoker` を設定します。Cloud Run IAMがOIDC認証を検証します。画面や公開APIにはタスク用エンドポイントを追加しません。

IAPのGoogle管理OAuthクライアントは通常、同一組織内のユーザー向けです。プロジェクトが`data-cloud.jp`と別組織にある場合、[Cloud Run IAPの外部ユーザー設定](https://docs.cloud.google.com/run/docs/securing/identity-aware-proxy-cloud-run)に従いカスタムOAuthクライアントを構成してください。IAMの`domain:`許可はWorkspaceの顧客IDで評価されるため、セカンダリドメインにも及び得ます。アプリはJWTのメールアドレスが`@data-cloud.jp`に一致するかを別途検査します。

インデックスの作成は非同期です。`gcloud firestore indexes composite list --project=PROJECT_ID`で全て`READY`になってから利用してください。独自ドメインを設定済みなら`.env`に`DEPLOY_BASE_URL=https://your-domain.example`を設定します。

FirestoreへはサーバーSDKとIAMでアクセスします。ブラウザー用Firebase SDKは使用しません。`firestore.rules`はブラウザーからのアクセスを全面拒否する参考設定です。既存のFirebaseプロジェクトへ導入する場合は既存ルールを確認し、意図せず公開しないでください。

### 既存環境を更新するとき

星による人気順とCloud Tasksを初めて導入する環境では、次の順で更新してください。画像要約だけを追加する更新では、追加のAPI・IAM・Firestoreインデックスやデータ移行は不要です。

1. `.env` の `PROJECT_ID`・`REGION`・`SERVICE` と `allow_accounts.yaml` を確認します。ADCは星の初期化に使います。

   ```bash
   gcloud auth login
   gcloud auth application-default login
   make bootstrap
   ```

2. Firestoreインデックスが利用可能になるまで待ちます。`YOUR_PROJECT_ID` は実際のプロジェクトIDに置き換え、すべて `READY` になったことを確認してください。`make bootstrap` は `make indexes` 相当の処理も実行します。インデックスだけを作り直す場合は `make indexes` を使えます。

   ```bash
   gcloud firestore indexes composite list --project=YOUR_PROJECT_ID
   ```

3. 古い記事の星の合計を初期化してから、デプロイします。

   ```bash
   make migrate-ratings
   make deploy
   ```

`make migrate-ratings` は `.env` の `PROJECT_ID` とADCを使い、Firestore Emulatorの設定を外してデプロイ先のDBを更新します。`rating_total` がない記事だけを0で初期化し、既存の星の合計は変更しません。再実行できます。初期化前の古い記事は、Firestoreの並べ替え対象に含まれないため人気一覧に表示されません。移行コマンドは `make deploy` から自動では実行しません。

`make deploy` はCloud Tasksキューと専用ワーカーを作成・更新し、ワーカーの呼び出し権限を設定してから画面・トークンAPIを更新します。新規のCloud Tasks構成は `make bootstrap` と `make deploy` に含まれています。

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

- ブックマークと最初の要約ジョブをFirestoreのトランザクションで同時に保存します。本番の配信・再試行・実行権については「[Cloud Tasksによる非同期処理](#cloud-tasksによる非同期処理)」を参照してください。
- 要約生成はユーザー単位で1分3件・UTC日付で1日50件まで。初回の自動キュー登録とWebからの明示的な再試行をFirestoreトランザクションで制限します。失敗した試行もカウントします。上限に達してもブックマークは保存され、後から要約を作成できます。
- 人気順は全ユーザーの星の合計順です。保存人数や期間別トレンドによるランキングではありません。
- 一覧は20件ずつ、詳細の公開ブックマークは新しい順に最大50件です。
- 要約の編集方針は`internal/content/prompt.go`の`SummaryInstruction()`で変更できます。構造化出力とサーバー側検証で1〜5項目を必須にします。
- 記事・画像の取得は公開HTTP/HTTPSの標準ポートのみ。DNS解決後のIPへ接続し、内部IP・リダイレクト・サイズ・時間を検証します。
- 本文が短い、またはGeminiが内容不足と判定した場合は、安全なHTTPクライアントで取得したHTMLをChromiumで描画し、動的表示の本文を抽出します。ページ内リンクは元ページから直接リンクされた公開HTMLを最大3件まで取得します。子ページから先へは進みません。
- Chromiumには取得済みHTMLを渡し、ブラウザーの外部通信を遮断します。関連ページURLもSSRF対策つきHTTPクライアントで再検証します。
- JavaScript外部配信に依存するSPA、ログインが必要な記事、取得を拒否するサイト、PDFには対応しません。取得に失敗してもブックマークは保持します。
- 要約は保存後に生成されるため、取得前のタイトルにはドメイン名を表示します。AI出力はHTMLとして実行しません。

## 確認する項目

実環境で許可アカウントのアクセス、許可外アカウントの拒否、URL保存→要約→編集→削除を確認してください。IAPの設定、モデル利用可否、請求・クォータは利用プロジェクトに依存します。IAPログアウト後もGoogle側のセッションが有効なら自動で再ログインする場合があります。

## 実装確認

2026-10-10時点で、今回の変更について次を実行し、成功を確認しています。

- `make test vet build`：race検出付きテスト、静的解析、サーバービルド。
- `make test-integration`：Firestore Emulatorを使う保存・所有者チェック・評価・理解状態・要約ジョブなどの統合テストと、既存のブラウザテスト。
- `TestImageSummaryFunctionalFlow`：画像要約の11ケースを個別にも実行。
- `git diff --check`：差分の空白エラー確認。

画像抽出・取得制限・Geminiへの画像データ送信・再描画時の画像取得上限には個別のテストもあります。初期実装では、署名付きテストIDトークンによる認証フローと、PC 1365px・モバイル390px / 320pxの画面を確認しています。

今回の変更に対する実際のGoogleログイン・Vertex AIによる記事／画像／YouTubeの要約・Cloud Tasksの実配信・Cloud Build / Cloud Runの本番デプロイは未実施です。モデル利用可否、要約の精度、実環境の認証・IAM・クォータはデプロイ先で別途確認してください。

参考: [Gemini 3.8 Flash](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash)、[構造化出力](https://ai.google.dev/gemini-api/docs/structured-output)、[Cloud Runのソースデプロイ](https://cloud.google.com/run/docs/deploying-source-code)。

## 専用トークンでURLを登録するAPI

ログイン後、サイドバーの **APIトークン** を開き「トークンを発行」を押します。
トークンは発行直後に一度だけ表示され、90日間有効です。1ユーザーにつき1つで、
再発行すると旧トークンが無効になります。「トークンを失効」でも停止できます。
Firestoreの `api_tokens` にはトークンのSHA-256ハッシュ・所有者・メールアドレス・有効期限を保存し、平文トークンは保存しません。

画面に表示されるAPIエンドポイントへ、`x-shiori-api` ヘッダーまたはURLのクエリパラメータ `token` でトークンを送信できます。
どちらか一方に1つだけ指定してください。両方の同時指定や複数指定はHTTP 401（`token_ambiguous`）になります。
`SHIORI_TOKEN` は発行したトークン、`SHIORI_API_URL` は画面のAPIエンドポイントです。

ヘッダー方式（URLにトークンを含めません）：

```bash
curl --request POST "${SHIORI_API_URL}" \
  --header "x-shiori-api: ${SHIORI_TOKEN}" \
  --header 'Content-Type: application/json' \
  --data '{"url":"https://example.com/article","comment":"あとで読む","tags":["技術","Go"]}'
```

URLパラメータ方式：

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

`make deploy` は同じイメージから3つのCloud Runサービスを構築します。

- `${SERVICE}`：IAPで保護した画面。トークンの発行・失効はログインとCSRF検証が必要です。
- `${SERVICE}-api`：`API_ONLY=true` のAPI専用サービス。Cloud Runの入口は公開し、アプリで専用トークンを検証します。画面・OAuth・トークン発行ルートは公開しません。保存した記事の要約は専用ワーカーが処理します。
- `${SERVICE}-worker`：`WORKER_ONLY=true` のIAMで保護された要約サービス。Cloud Tasksからの依頼を処理します。

API側でも `allow_accounts.yaml` のメールアドレス／ドメインを毎リクエスト照合します。
許可リストの変更後は `make deploy` で再デプロイしてください。APIのURLは画面サービスの `API_BASE_URL` に自動設定されます。
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

- `token_missing`：URLの `token` パラメータと `x-shiori-api` ヘッダーのどちらもない、または指定した値が空です。
- `token_ambiguous`：両方式を同時に指定した、またはトークンを複数指定しています（カンマ区切りのヘッダーも含みます）。
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

発行直後の画面にはトークン全体と、発行した値を埋め込んだヘッダー版・URL版のcurlをそれぞれコピーするボタンがあります。
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
