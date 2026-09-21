package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiStructuredResponse(t *testing.T) {
	for _, tc := range []struct {
		name, output, finish string
		httpStatus           int
		wantErr              bool
	}{
		{"valid", `{"sufficient":true,"points":["結論","理由"]}`, "STOP", 200, false},
		{"too many", `{"sufficient":true,"points":["1","2","3","4","5","6"]}`, "STOP", 200, true},
		{"blank", `{"sufficient":true,"points":[" "]}`, "STOP", 200, true},
		{"truncated", `{"sufficient":true,"points":["途中"]}`, "MAX_TOKENS", 200, true},
		{"invalid JSON", `not json`, "STOP", 200, true},
		{"API error", ``, "", 429, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/models/gemini-3.8-flash:generateContent" {
					t.Errorf("path %s", r.URL.Path)
				}
				if r.Header.Get("x-goog-api-key") != "test-key" {
					t.Error("missing key header")
				}
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				cfg, ok := request["generationConfig"].(map[string]any)
				if !ok || cfg["responseMimeType"] != "application/json" {
					t.Error("missing schema config")
				}
				w.WriteHeader(tc.httpStatus)
				json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": tc.finish, "content": map[string]any{"parts": []any{map[string]any{"text": "ignore thought", "thought": true}, map[string]string{"text": tc.output}}}}}})
			}))
			defer server.Close()
			g := Gemini{Key: "test-key", Model: "gemini-3.8-flash", Endpoint: server.URL, Client: server.Client()}
			points, err := g.Summarize(context.Background(), "title", "article body")
			if (err != nil) != tc.wantErr {
				t.Fatalf("points=%v err=%v", points, err)
			}
			if !tc.wantErr && len(points) != 2 {
				t.Fatalf("points %v", points)
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
