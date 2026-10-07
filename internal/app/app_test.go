package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"my-bookmark/internal/iap"
	"my-bookmark/internal/store"
)

type testDB struct {
	Database
	entries []store.Entry
}

func (d testDB) List(context.Context, string, string, string) ([]store.Entry, string, error) {
	return d.entries, "", nil
}
func (d testDB) Session(context.Context, string) (store.Session, error) {
	return store.Session{User: store.User{ID: "user", Name: "reader"}, CSRF: "correct", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func testApp(t *testing.T) *App {
	t.Helper()
	a, err := New(Config{BaseURL: "http://localhost:8080", Env: "development"}, testDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func TestProtectedRoutes(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/bookmarks", "/bookmarks/abc/delete", "/articles/abc/summary", "/logout", "/settings/api/issue", "/settings/api/revoke"} {
		r := httptest.NewRequest("POST", path, strings.NewReader("csrf=correct"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
func TestCSRF(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("POST", "/bookmarks", strings.NewReader("csrf=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "token"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
}
func TestFeedEscapesSummary(t *testing.T) {
	a := testApp(t)
	a.db = testDB{entries: []store.Entry{{Article: store.Article{ID: strings.Repeat("a", 64), URL: "https://example.com/", Title: "記事", Domain: "example.com", Status: "ready", Points: []string{"<script>alert(1)</script>"}, Count: 1}}}}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<script>alert(1)</script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("summary not escaped")
	}
	if !strings.Contains(w.Body.String(), "name=\"viewport\"") {
		t.Fatal("missing mobile viewport")
	}
}
func TestBookmarkInput(t *testing.T) {
	good := url.Values{"url": {"https://example.com"}, "comment": {"コメント"}, "tags": {"go, AI,go"}}
	_, _, tags, err := bookmarkInput(good)
	if err != nil || len(tags) != 2 {
		t.Fatalf("%v %v", tags, err)
	}
	good.Set("comment", strings.Repeat("あ", 501))
	if _, _, _, err = bookmarkInput(good); err == nil {
		t.Fatal("long comment accepted")
	}
}
func TestConfigRejectsUnsafeProduction(t *testing.T) {
	c := Config{Env: "production", BaseURL: "http://localhost:8080", Project: "test", GoogleClientID: "id", GoogleClientSecret: "secret", GeminiModel: "gemini-3.8-flash"}
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted http")
	}
}

func TestConfigAllowsProductionWithoutGeminiAPIKey(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	list, err := iap.ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Env: "production", BaseURL: "https://bookmark.example", Project: "test", IAPAudience: "/projects/123/locations/asia-northeast1/services/shiori", IAPAllowlist: list, GeminiModel: "gemini-3.8-flash", GeminiLocation: "global"}
	if err := c.Validate(); err != nil {
		t.Fatalf("production requires an API key: %v", err)
	}
}

func TestProductionRequiresIAP(t *testing.T) {
	c := Config{Env: "production", BaseURL: "https://bookmark.example", Project: "test", GoogleClientID: "id", GoogleClientSecret: "secret"}
	if err := c.Validate(); err == nil {
		t.Fatal("production accepted without IAP")
	}
}
