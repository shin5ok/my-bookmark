package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestExtractSingleAPIURL(t *testing.T) {
	tests := []struct {
		name, input, want string
		invalid           bool
	}{
		{name: "plain", input: "https://example.com/post?q=1&lang=ja", want: "https://example.com/post?q=1&lang=ja"},
		{name: "prose", input: "あとで読む: https://example.com/post?q=1&lang=ja この記事です", want: "https://example.com/post?q=1&lang=ja"},
		{name: "markdown", input: "[記事](https://example.com/post).", want: "https://example.com/post"},
		{name: "Japanese punctuation", input: "URLはこちら「HTTPS://Example.COM/a」。", want: "https://example.com/a"},
		{name: "balanced parenthesis", input: "https://example.com/wiki/Function_(math)", want: "https://example.com/wiki/Function_(math)"},
		{name: "ambiguous", input: "https://example.com/one と https://example.org/two", invalid: true},
		{name: "missing", input: "記事だけでURLはありません", invalid: true},
		{name: "internal", input: "記事: http://127.0.0.1/admin", invalid: true},
		{name: "embedded credential", input: "記事: https://user:pass@example.com/path", invalid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractSingleAPIURL(tc.input)
			if (err != nil) != tc.invalid || (!tc.invalid && got != tc.want) {
				t.Fatalf("got=%q err=%v, want=%q invalid=%t", got, err, tc.want, tc.invalid)
			}
		})
	}
}
func TestAPIExtractsURLFromJSONText(t *testing.T) {
	for _, method := range []string{"POST", "PUT"} {
		t.Run(method, func(t *testing.T) {
			a, db, rawToken := apiFixture(t)
			body := `{"url":"共有: [記事](https://example.com/post?q=1&lang=ja)。ぜひ読んでください","tags":["技術"]}`
			request := httptest.NewRequest(method, "/api/bookmarks?token="+url.QueryEscape(rawToken), strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK || db.savedURL != "https://example.com/post?q=1&lang=ja" || len(db.tags) != 1 || db.tags[0] != "技術" {
				t.Fatalf("status=%d saved=%q tags=%v body=%s", response.Code, db.savedURL, db.tags, response.Body.String())
			}
		})
	}
}
func TestAPIDoesNotSaveAmbiguousOrPrivateURL(t *testing.T) {
	for _, input := range []string{"https://example.com and https://example.org", "see http://127.0.0.1/private"} {
		a, db, token := apiFixture(t)
		body := `{"url":"` + input + `"}`
		request := httptest.NewRequest("POST", "/api/bookmarks?token="+url.QueryEscape(token), strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || db.savedURL != "" {
			t.Fatalf("input=%q status=%d saved=%q", input, response.Code, db.savedURL)
		}
	}
}
