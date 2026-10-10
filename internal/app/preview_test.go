package app

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"my-bookmark/internal/store"
)

// A test-only preview. It cannot be compiled into cmd/server or used to bypass login.
func TestPreviewServer(t *testing.T) {
	if os.Getenv("SHIORI_PREVIEW") != "1" {
		t.Skip("run make preview for visual QA")
	}
	now := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	entries := []store.Entry{
		{Article: store.Article{ID: strings.Repeat("a", 64), URL: "https://example.com/serverless", Title: "小さく始めるWebサービス。サーバーレスで考える、これからの設計", Domain: "engineering.example.com", Count: 128, CreatedAt: now, Status: "ready", Points: []string{"サーバーレスでは、アクセス量に合わせて実行環境が自動で増減。小さなサービスでも運用負担を抑えて始められる。", "データを実行環境の外に保存する設計が重要。リクエストをまたぐ状態はデータベースで管理する。", "処理時間と費用のバランスを見ながら、まずはシンプルな構成で運用し、必要に応じて役割を分ける。"}}},
		{Article: store.Article{ID: strings.Repeat("b", 64), URL: "https://example.com/design", Title: "「読みやすい」をつくる。余白と文字から見直すインターフェース", Domain: "design.example.com", Count: 86, CreatedAt: now, Status: "ready", Points: []string{"情報を詰め込まず、関連する内容を余白でまとめることで、読者が自然に情報のまとまりを理解できる。", "モバイルでは文字サイズだけでなく、行間・1行の長さ・操作領域も合わせて調整する。", "装飾を加える前に、見出しと本文の役割を整理。重要な情報が最初に目に入る構成にする。"}}},
		{Article: store.Article{ID: strings.Repeat("c", 64), URL: "https://example.com/reading", Title: "読みたい記事をため込まない、小さな読書習慣のつくり方", Domain: "journal.example.com", Count: 42, CreatedAt: now, Status: "ready", Points: []string{"まず要点に目を通し、深く読みたい記事を選ぶと、限られた時間を使いやすくなる。", "気になった理由を短いコメントに残しておくと、あとで記事を探す手がかりになる。"}}},
	}
	a, err := New(Config{BaseURL: "http://localhost:8090", Env: "development"}, previewDB{testDB{entries: entries}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Test-only sample preview: http://localhost:8090/new (all articles are fictional)")
	if err := http.ListenAndServe("127.0.0.1:8090", a.Handler()); err != nil {
		t.Fatal(err)
	}
}

type previewDB struct{ testDB }

func (d previewDB) Session(context.Context, string) (store.Session, error) {
	return store.Session{}, store.ErrNotFound
}
func (d previewDB) Get(_ context.Context, id string) (store.Article, error) {
	for _, e := range d.entries {
		if e.Article.ID == id {
			return e.Article, nil
		}
	}
	return store.Article{}, store.ErrNotFound
}
func (d previewDB) Comments(context.Context, string) ([]store.Bookmark, error) { return nil, nil }
