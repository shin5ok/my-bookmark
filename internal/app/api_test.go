package app

import (
	"context"
	"encoding/json"
	"errors"
	"my-bookmark/internal/iap"
	"my-bookmark/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAPIRequiresTokenEvenWithSessionCookie(t *testing.T) {
	a := testApp(t)
	for _, method := range []string{"POST", "PUT"} {
		r := httptest.NewRequest(method, "/api/bookmarks", strings.NewReader(`{"url":"https://example.com"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "cookie"})
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d, want 401", method, w.Code)
		}
	}
}

type apiTestDB struct {
	testDB
	token              store.APIToken
	owner              store.User
	savedURL, comment  string
	tags               []string
	lookupErr, saveErr error
}

func (d *apiTestDB) APIToken(_ context.Context, uid string) (store.APIToken, error) {
	if d.lookupErr != nil {
		return store.APIToken{}, d.lookupErr
	}
	if d.token.User.ID != uid {
		return store.APIToken{}, store.ErrNotFound
	}
	return d.token, nil
}
func (d *apiTestDB) PutAPIToken(_ context.Context, token store.APIToken) error {
	d.token = token
	return nil
}
func (d *apiTestDB) DeleteAPIToken(_ context.Context, uid string) error {
	if d.token.User.ID == uid {
		d.token = store.APIToken{}
	}
	return nil
}
func (d *apiTestDB) Save(_ context.Context, u store.User, raw, comment string, tags []string) (store.Article, error) {
	d.owner = u
	d.savedURL = raw
	d.comment = comment
	d.tags = tags
	return store.Article{ID: store.Hash(raw), URL: raw, Status: "queued"}, d.saveErr
}
func (d *apiTestDB) Session(context.Context, string) (store.Session, error) {
	return store.Session{User: store.User{ID: store.Hash("owner"), Name: "reader"}, Email: "reader@data-cloud.jp", CSRF: "correct"}, nil
}
func apiFixture(t *testing.T) (*App, *apiTestDB, string) {
	t.Helper()
	uid := store.Hash("owner")
	raw := uid + "." + store.RandomToken()
	list, err := iap.ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	db := &apiTestDB{token: store.APIToken{User: store.User{ID: uid, Name: "reader"}, Email: "reader@data-cloud.jp", Hash: store.Hash(raw), ExpiresAt: time.Now().Add(time.Hour)}}
	a, err := New(Config{Env: "production", BaseURL: "https://shiori.example", APIOnly: true, IAPAllowlist: list}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a, db, raw
}
func TestAPISavesForTokenOwner(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			a, db, raw := apiFixture(t)
			r := httptest.NewRequest(method, "/api/bookmarks?token="+raw, strings.NewReader(`{"url":"https://example.com/article#section","comment":" note ","tags":["go","go"," AI "]}`))
			r.Header.Set("Content-Type", "application/json")
			r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "unrelated-user"})
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if db.owner.ID != store.Hash("owner") || db.savedURL != "https://example.com/article" || db.comment != "note" || strings.Join(db.tags, ",") != "go,AI" {
				t.Fatalf("incorrect save: %+v", db)
			}
			var result map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result["id"] != store.Hash(db.savedURL) || result["status"] != "queued" {
				t.Fatalf("response=%v", result)
			}
			if w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), raw) {
				t.Fatal("API leaked credentials or created cookie")
			}
		})
	}
}
func TestAPIRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
		mutate                  func(*apiTestDB, *http.Request)
	}{
		{name: "wrong token", status: 401, mutate: func(d *apiTestDB, r *http.Request) { d.token.Hash = store.Hash("other") }},
		{name: "expired", status: 401, mutate: func(d *apiTestDB, r *http.Request) { d.token.ExpiresAt = time.Now().Add(-time.Second) }},
		{name: "revoked", status: 401, mutate: func(d *apiTestDB, r *http.Request) { d.token = store.APIToken{} }},
		{name: "removed account", status: 403, mutate: func(d *apiTestDB, r *http.Request) { d.token.Email = "x@other.jp" }},
		{name: "duplicate token", status: 401, mutate: func(d *apiTestDB, r *http.Request) { r.URL.RawQuery += "&token=other" }},
		{name: "lookup failure", status: 503, mutate: func(d *apiTestDB, r *http.Request) { d.lookupErr = errors.New("db unavailable") }},
		{name: "wrong content type", status: 415, contentType: "text/plain"},
		{name: "invalid URL", status: 400, body: `{"url":"http://127.0.0.1"}`},
		{name: "unknown field", status: 400, body: `{"url":"https://example.com","user_id":"victim"}`},
		{name: "trailing JSON", status: 400, body: `{"url":"https://example.com"} {}`},
		{name: "too large", status: 400, body: `{"url":"https://example.com","comment":"` + strings.Repeat("x", 17000) + `"}`},
		{name: "too many tags", status: 400, body: `{"url":"https://example.com","tags":["a","b","c","d","e","f"]}`},
		{name: "comma in tag", status: 400, body: `{"url":"https://example.com","tags":["a,b"]}`},
		{name: "long comment", status: 400, body: `{"url":"https://example.com","comment":"` + strings.Repeat("あ", 501) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, db, raw := apiFixture(t)
			body := tc.body
			if body == "" {
				body = `{"url":"https://example.com"}`
			}
			r := httptest.NewRequest("POST", "/api/bookmarks?token="+raw, strings.NewReader(body))
			contentType := tc.contentType
			if contentType == "" {
				contentType = "application/json"
			}
			r.Header.Set("Content-Type", contentType)
			if tc.mutate != nil {
				tc.mutate(db, r)
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || db.savedURL != "" {
				t.Fatalf("got %d, want %d; saved=%s; %s", w.Code, tc.status, db.savedURL, w.Body.String())
			}
		})
	}
}
func TestAPIOnlyDoesNotExposeBrowserRoutes(t *testing.T) {
	a, _, raw := apiFixture(t)
	for _, path := range []string{"/", "/mine", "/settings/api", "/settings/api/issue", "/static/app.js", "/auth/google", "/bookmarks"} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("GET", path+"?token="+raw, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/bookmarks?token="+raw, nil))
	if w.Code != 405 {
		t.Fatalf("GET: %d", w.Code)
	}
}
func TestTokenIssueRotateAndRevoke(t *testing.T) {
	a, db, old := apiFixture(t)
	a.cfg.APIOnly = false
	a.cfg.Env = "development"
	a.cfg.BaseURL = "http://localhost:8080"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "session"})
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/settings/api/issue", "/settings/api/revoke"} {
		if w := request("POST", path, "csrf=wrong"); w.Code != 403 {
			t.Fatalf("CSRF: %d", w.Code)
		}
	}
	w := request("POST", "/settings/api/issue", "csrf=correct")
	if w.Code != 200 {
		t.Fatalf("issue: %d %s", w.Code, w.Body.String())
	}
	match := regexp.MustCompile(`value="([a-f0-9]{64}\.[A-Za-z0-9_-]{43})"`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 {
		t.Fatal("issued token not displayed")
	}
	raw := match[1]
	if raw == old || db.token.Hash != store.Hash(raw) || strings.Contains(db.token.Hash, raw) || db.token.Email != "reader@data-cloud.jp" {
		t.Fatal("token replacement/hash/owner wrong")
	}
	if db.token.ExpiresAt.Before(time.Now().Add(89 * 24 * time.Hour)) {
		t.Fatal("expiry not set")
	}
	w = request("GET", "/settings/api", "")
	if strings.Contains(w.Body.String(), raw) {
		t.Fatal("token shown twice")
	}
	call := func(token string) int {
		r := httptest.NewRequest("PUT", "/api/bookmarks?token="+token, strings.NewReader(`{"url":"https://example.com"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if call(old) != 401 || call(raw) != 200 {
		t.Fatal("rotation ineffective")
	}
	if w = request("POST", "/settings/api/revoke", "csrf=correct"); w.Code != 303 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if call(raw) != 401 {
		t.Fatal("revoked token accepted")
	}
}

func TestAPIConfiguration(t *testing.T) {
	list, err := iap.ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	cfg := Config{Env: "production", Project: "test", BaseURL: "https://shiori.example", APIOnly: true, IAPAllowlist: list}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.APIOnly = false
	if err := cfg.Validate(); err == nil {
		t.Fatal("web accepted without IAP")
	}
	cfg.APIOnly = true
	cfg.IAPAllowlist = iap.Allowlist{}
	if err := cfg.Validate(); err == nil {
		t.Fatal("API accepted without allowlist")
	}
	cfg.IAPAllowlist = list
	for _, endpoint := range []string{"http://example.com", "https://user:pass@example.com", "https://example.com/?token=x", "https://example.com/path"} {
		cfg.APIBaseURL = endpoint
		if err := cfg.Validate(); err == nil {
			t.Fatalf("unsafe API endpoint accepted: %s", endpoint)
		}
	}
}
func TestWebProductionDoesNotExposeTokenAPI(t *testing.T) {
	a, _, _ := apiFixture(t)
	a.cfg.APIOnly = false
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/bookmarks", nil))
	if w.Code != 404 {
		t.Fatalf("web API status: %d", w.Code)
	}
}

func TestTokenFormsRejectUntrustedOrigins(t *testing.T) {
	for _, origin := range []string{"null", "https://other.example"} {
		for _, path := range []string{"/settings/api/issue", "/settings/api/revoke"} {
			t.Run(origin+path, func(t *testing.T) {
				a, db, _ := apiFixture(t)
				a.cfg.APIOnly = false
				a.cfg.Env = "development"
				a.cfg.BaseURL = "http://localhost:8080"
				originalHash := db.token.Hash
				r := httptest.NewRequest("POST", path, strings.NewReader("csrf=correct"))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("Origin", origin)
				r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "session"})
				w := httptest.NewRecorder()
				a.Handler().ServeHTTP(w, r)
				if w.Code != 403 || db.token.Hash != originalHash {
					t.Fatalf("untrusted origin changed token: status=%d", w.Code)
				}
			})
		}
	}
}

func TestAPITokenErrorsExplainRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, query, code string
		mutate            func(*apiTestDB)
	}{
		{name: "missing", query: "", code: "token_missing"},
		{name: "empty", query: "token=", code: "token_missing"},
		{name: "incomplete copy", query: "token=short", code: "token_malformed"},
		{name: "duplicate", query: "token=TOKEN&token=TOKEN", code: "token_ambiguous"},
		{name: "expired", query: "token=TOKEN", code: "token_expired", mutate: func(d *apiTestDB) { d.token.ExpiresAt = time.Now().Add(-time.Second) }},
		{name: "old token", query: "token=TOKEN", code: "token_invalid", mutate: func(d *apiTestDB) { d.token.Hash = store.Hash("replacement") }},
		{name: "revoked", query: "token=TOKEN", code: "token_invalid", mutate: func(d *apiTestDB) { d.token = store.APIToken{} }},
		{name: "wrong expired token", query: "token=TOKEN", code: "token_invalid", mutate: func(d *apiTestDB) {
			d.token.Hash = store.Hash("replacement")
			d.token.ExpiresAt = time.Now().Add(-time.Second)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, db, raw := apiFixture(t)
			if tc.mutate != nil {
				tc.mutate(db)
			}
			r := httptest.NewRequest("POST", "/api/bookmarks?"+strings.ReplaceAll(tc.query, "TOKEN", raw), strings.NewReader(`{"url":"https://example.com"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			var response map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != 401 || response["code"] != tc.code || response["message"] == "" {
				t.Fatalf("status=%d response=%v", w.Code, response)
			}
			if db.savedURL != "" || strings.Contains(w.Body.String(), raw) {
				t.Fatal("invalid token saved data or leaked token")
			}
		})
	}
}

func TestAPIAcceptsCopiedTokenWhitespace(t *testing.T) {
	a, _, raw := apiFixture(t)
	r := httptest.NewRequest("POST", "/api/bookmarks?token="+url.QueryEscape(" \r\n"+raw+"\t "), strings.NewReader(`{"url":"https://example.com/article"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("copied token: %d %s", w.Code, w.Body.String())
	}
}
