package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"my-bookmark/internal/summary"
)

func TestGeminiStructuredResponse(t *testing.T) {
	for _, tc := range []struct {
		name, output, finish string
		httpStatus           int
		wantErr              bool
	}{
		{"valid", `{"sufficient":true,"title":"新しい技術の要点","tldr":["結論の短文","重要性の短文","影響の短文"],"points":["結論","理由"]}`, "STOP", 200, false},
		{"insufficient", `{"sufficient":false,"title":"短い日本語タイトル","tldr":[],"points":[]}`, "STOP", 200, false},
		{"insufficient invalid title", `{"sufficient":false,"title":"<不正なタイトル>","tldr":["根拠のない短文"],"points":["根拠のない要約"]}`, "STOP", 200, false},
		{"invalid title", `{"sufficient":true,"title":"An English title","tldr":["結論の短文","重要性の短文","影響の短文"],"points":["結論","理由"]}`, "STOP", 200, false},
		{"long title", `{"sufficient":true,"title":"` + strings.Repeat("長", 61) + `","tldr":["結論の短文","重要性の短文","影響の短文"],"points":["結論","理由"]}`, "STOP", 200, false},
		{"missing TLDR", `{"sufficient":true,"title":"日本語タイトル","points":["結論","理由"]}`, "STOP", 200, true},
		{"one TLDR line", `{"sufficient":true,"title":"日本語タイトル","tldr":["結論"],"points":["結論","理由"]}`, "STOP", 200, true},
		{"too many", `{"sufficient":true,"points":["1","2","3","4","5","6"]}`, "STOP", 200, true},
		{"blank", `{"sufficient":true,"points":[" "]}`, "STOP", 200, true},
		{"truncated", `{"sufficient":true,"points":["途中"]}`, "MAX_TOKENS", 200, true},
		{"invalid JSON", `not json`, "STOP", 200, true},
		{"API error", ``, "", 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/projects/test-project/locations/global/publishers/google/models/gemini-3.8-flash:generateContent" {
					t.Errorf("path %s", r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("missing ADC bearer token")
				}
				if r.Header.Get("x-goog-api-key") != "" {
					t.Error("API key header must not be sent")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				cfg, ok := request["generationConfig"].(map[string]any)
				if !ok || cfg["responseMimeType"] != "application/json" {
					t.Error("missing schema config")
				}
				if schema, ok := cfg["responseJsonSchema"].(map[string]any); ok {
					if properties, ok := schema["properties"].(map[string]any); !ok || properties["title"] == nil || properties["tldr"] == nil {
						t.Error("response schema must request a title")
					}
				} else {
					t.Error("missing response schema")
				}
				w.WriteHeader(tc.httpStatus)
				json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": tc.finish, "content": map[string]any{"parts": []any{map[string]any{"text": "ignore thought", "thought": true}, map[string]string{"text": tc.output}}}}}})
			}))
			defer server.Close()
			g := Gemini{
				Project:     "test-project",
				Location:    "global",
				Model:       "gemini-3.8-flash",
				TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}),
				Endpoint:    server.URL,
				Client:      server.Client(),
			}
			result, err := g.Summarize(context.Background(), "title", "article body", summary.Standard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("result=%v err=%v", result, err)
			}
			if strings.HasPrefix(tc.name, "insufficient") {
				wantTitle := "短い日本語タイトル"
				if tc.name == "insufficient invalid title" {
					wantTitle = ""
				}
				if result.Title != wantTitle || len(result.Points) != 0 || len(result.TLDR) != 0 {
					t.Fatalf("insufficient content must retain only a valid title: %+v", result)
				}
				return
			}
			if !tc.wantErr && len(result.Points) != 2 {
				t.Fatalf("points %v", result.Points)
			}
			if !tc.wantErr && len(result.TLDR) != 3 {
				t.Fatalf("TLDR: %v", result.TLDR)
			}
			if tc.name == "valid" && result.Title != "新しい技術の要点" {
				t.Fatalf("title %q", result.Title)
			}
			if (tc.name == "invalid title" || tc.name == "long title") && result.Title != "" {
				t.Fatalf("invalid title should fall back: %q", result.Title)
			}
		})
	}
}
func TestFetcherBlocksLoopbackAndRedirect(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/", "http://[::1]/", "http://localhost/"} {
		if _, _, err := Fetch(context.Background(), NewFetcher(), raw); err == nil {
			t.Errorf("allowed %s", raw)
		}
	}
	req := httptest.NewRequest("GET", "http://169.254.169.254/latest/meta-data/", nil)
	if err := NewFetcher().CheckRedirect(req, []*http.Request{}); err == nil {
		t.Fatal("metadata redirect allowed")
	}
}
func TestFetchLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body, ctype string
		status            int
	}{
		{"size", strings.Repeat("x", 2*1024*1024+1), "text/plain", 200},
		{"type", "binary", "application/octet-stream", 200},
		{"short", "<p>short</p>", "text/html", 200},
		{"http error", "error", "text/html", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				w.Header().Set("Content-Type", tc.ctype)
				w.WriteHeader(tc.status)
				w.WriteString(tc.body)
				return w.Result(), nil
			})}
			if _, _, err := Fetch(context.Background(), client, "https://example.com/"); err == nil {
				t.Fatal("accepted invalid article")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
