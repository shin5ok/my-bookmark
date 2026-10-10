package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAPIAcceptsHeaderToken(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			a, db, raw := apiFixture(t)
			r := httptest.NewRequest(method, "/api/bookmarks", strings.NewReader(`{"url":"https://example.com/article","comment":"ヘッダーで登録"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("x-shiori-api", " \t"+raw+" ")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK || db.savedURL != "https://example.com/article" || db.owner.ID != db.token.User.ID || db.comment != "ヘッダーで登録" {
				t.Fatalf("header save: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAPIRejectsAmbiguousOrInvalidHeaderTokens(t *testing.T) {
	for _, tc := range []struct {
		name, query    string
		headers        []string
		expire, revoke bool
		code           string
	}{
		{name: "empty header", headers: []string{" "}, code: "token_missing"},
		{name: "malformed header", headers: []string{"short"}, code: "token_malformed"},
		{name: "invalid header", headers: []string{"WRONG"}, code: "token_invalid"},
		{name: "expired header", headers: []string{"TOKEN"}, expire: true, code: "token_expired"},
		{name: "revoked header", headers: []string{"TOKEN"}, revoke: true, code: "token_invalid"},
		{name: "same token in both", query: "token=TOKEN", headers: []string{"TOKEN"}, code: "token_ambiguous"},
		{name: "different token in both", query: "token=TOKEN", headers: []string{"short"}, code: "token_ambiguous"},
		{name: "empty query plus header", query: "token=", headers: []string{"TOKEN"}, code: "token_ambiguous"},
		{name: "duplicate header", headers: []string{"TOKEN", "TOKEN"}, code: "token_ambiguous"},
		{name: "combined header", headers: []string{"TOKEN,TOKEN"}, code: "token_ambiguous"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, db, raw := apiFixture(t)
			if tc.expire {
				db.token.ExpiresAt = time.Now().Add(-time.Second)
			}
			if tc.revoke {
				db.token.Hash = "revoked"
			}
			target := "/api/bookmarks"
			if tc.query != "" {
				target += "?" + strings.ReplaceAll(tc.query, "TOKEN", url.QueryEscape(raw))
			}
			r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{"url":"https://example.com/article"}`))
			r.Header.Set("Content-Type", "application/json")
			for _, header := range tc.headers {
				r.Header.Add("x-shiori-api", strings.NewReplacer("TOKEN", raw, "WRONG", strings.Repeat("b", 64)+"."+strings.Repeat("x", 43)).Replace(header))
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			var result map[string]string
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 401 || result["code"] != tc.code || result["message"] == "" {
				t.Fatalf("response=%d %v", w.Code, result)
			}
			if db.savedURL != "" || strings.Contains(w.Body.String(), raw) {
				t.Fatal("invalid credentials saved article or leaked token")
			}
		})
	}
}
