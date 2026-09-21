package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"my-bookmark/internal/store"
)

type summaryStub struct{ points []string }

func (s summaryStub) Summarize(context.Context, string, string) ([]string, error) {
	return s.points, nil
}

type articleTransport struct{}

func (articleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteString(`<html><title>検証用の記事</title><article><p>` + strings.Repeat("これは非同期ブックマーク保存と要約ワーカーのテスト用の記事本文です。実行進捗を画面で確認し、保存後に結果を一覧へ表示します。", 16) + `</p></article></html>`)
	return w.Result(), nil
}
func TestBookmarkHTTPFlow(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	db := store.New(c)
	user := store.User{ID: store.RandomToken(), Name: "test reader"}
	token := store.RandomToken()
	csrf := store.RandomToken()
	if err = db.PutSession(ctx, token, store.Session{User: user, CSRF: csrf, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	defer db.DeleteSession(ctx, token)
	a, err := New(Config{BaseURL: "http://localhost:8080", Env: "development", GeminiModel: "gemini-3.8-flash"}, db, summaryStub{[]string{"結論です。", "理由です。"}})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(db, summaryStub{[]string{"結論です。", "理由です。"}}, "gemini-3.8-flash")
	worker.fetcher = &http.Client{Transport: articleTransport{}}
	go worker.Run(ctx)
	request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(values.Encode())).WithContext(ctx)
		r.AddCookie(&http.Cookie{Name: "shiori_session", Value: token})
		if method == "POST" {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	raw := "https://example.com/" + store.RandomToken()
	aid := store.Hash(raw)
	saved := request("POST", "/bookmarks", url.Values{"csrf": {csrf}, "url": {raw}, "comment": {"最初のコメント"}, "tags": {"Go,AI"}})
	if saved.Code != 303 || saved.Header().Get("Location") != "/articles/"+aid {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	defer db.Delete(ctx, user.ID, aid)
	detail := request("GET", "/articles/"+aid, nil)
	if detail.Code != 200 || !strings.Contains(detail.Body.String(), "最初のコメント") {
		t.Fatalf("detail: %d", detail.Code)
	}
	summary := request("POST", "/articles/"+aid+"/summary", url.Values{"csrf": {csrf}})
	if summary.Code != http.StatusAccepted {
		t.Fatalf("async summary enqueue: %d %s", summary.Code, summary.Body.String())
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		article, err := db.Get(ctx, aid)
		if err != nil {
			t.Fatal(err)
		}
		if article.Status == "ready" {
			break
		}
		if article.Status == "failed" {
			t.Fatalf("summary failed: %s", article.LastError)
		}
		time.Sleep(100 * time.Millisecond)
	}
	article, err := db.Get(ctx, aid)
	if err != nil || article.Status != "ready" || len(article.Points) != 2 {
		t.Fatalf("async result: %#v %v", article, err)
	}

	feed := request("GET", "/mine", nil)
	if feed.Code != 200 || !strings.Contains(feed.Body.String(), "結論です。") {
		t.Fatal("summary missing from feed")
	}
	updated := request("POST", "/bookmarks", url.Values{"csrf": {csrf}, "url": {raw}, "comment": {"更新しました"}})
	if updated.Code != 303 {
		t.Fatal("update failed")
	}
	article, err = db.Get(ctx, aid)
	if err != nil || article.Count != 1 {
		t.Fatalf("count changed on edit: %#v %v", article, err)
	}
	deleted := request("POST", "/bookmarks/"+aid+"/delete", url.Values{"csrf": {csrf}})
	if deleted.Code != 303 {
		t.Fatal("delete failed")
	}

}
