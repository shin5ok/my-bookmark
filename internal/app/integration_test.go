package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
)

type summaryStub struct{ points []string }

func (s summaryStub) Summarize(context.Context, string, string, summary.Style, string) (summary.Result, error) {
	return summary.Result{Title: "簡潔な日本語タイトル", TLDR: []string{"結論です。", "重要性です。", "影響です。"}, Points: s.points}, nil
}

type articleTransport struct{}

func (articleTransport) RoundTrip(*http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteString(`<html><title>検証用の記事</title><article><p>` + strings.Repeat("これは非同期ブックマーク保存と要約ワーカーのテスト用の記事本文です。実行進捗を画面で確認し、保存後に結果を一覧へ表示します。", 32) + `</p></article></html>`)
	return w.Result(), nil
}
func TestBookmarkHTTPFlow(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Isolate the worker queue from store tests running in another package.
	c, err := firestore.NewClient(ctx, "demo-app-test-"+store.Hash(store.RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	db := store.New(c)
	user := store.User{ID: store.Hash(store.RandomToken()), Name: "test reader"}
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
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); worker.Run(workerCtx) }()
	defer func() { stopWorker(); <-workerDone }()
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
	if err != nil || article.Status != "ready" || len(article.Points) != 2 || len(article.TLDR) != 3 || article.Title != "簡潔な日本語タイトル" {
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

	issued := request("POST", "/settings/api/issue", url.Values{"csrf": {csrf}})
	match := regexp.MustCompile(`value="([a-f0-9]{64}\.[A-Za-z0-9_-]{43})"`).FindStringSubmatch(issued.Body.String())
	if issued.Code != 200 || len(match) != 2 {
		t.Fatalf("token issue failed: %d", issued.Code)
	}
	defer db.DeleteAPIToken(ctx, user.ID)
	for _, method := range []string{"POST", "PUT"} {
		req := httptest.NewRequest(method, "/api/bookmarks?token="+match[1], strings.NewReader(`{"url":"`+raw+`","comment":"APIで更新"}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, req)
		if response.Code != 200 {
			t.Fatalf("API save: %d %s", response.Code, response.Body.String())
		}
	}
	own, err := db.Own(ctx, user.ID, aid)
	if err != nil || own.Comment != "APIで更新" {
		t.Fatalf("API owner save: %+v %v", own, err)
	}
	article, err = db.Get(ctx, aid)
	if err != nil || article.Count != 1 {
		t.Fatal("API retry duplicated bookmark", err)
	}
	revoked := request("POST", "/settings/api/revoke", url.Values{"csrf": {csrf}})
	if revoked.Code != 303 {
		t.Fatalf("revoke: %d", revoked.Code)
	}
	req := httptest.NewRequest("POST", "/api/bookmarks?token="+match[1], strings.NewReader(`{"url":"`+raw+`"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.Handler().ServeHTTP(response, req)
	if response.Code != 401 {
		t.Fatalf("revoked API access: %d", response.Code)
	}
	deleted := request("POST", "/bookmarks/"+aid+"/delete", url.Values{"csrf": {csrf}})
	if deleted.Code != 303 {
		t.Fatal("delete failed")
	}

}
