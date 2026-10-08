package content

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
	"my-bookmark/internal/summary"
)

func TestYouTubeURL(t *testing.T) {
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=3KtWfp0UopM&t=20",
		"https://youtu.be/3KtWfp0UopM?si=tracking",
		"https://m.youtube.com/shorts/3KtWfp0UopM",
		"https://youtube.com/live/3KtWfp0UopM",
	} {
		got, recognized, err := YouTubeURL(raw)
		if err != nil || !recognized || got != "https://www.youtube.com/watch?v=3KtWfp0UopM" {
			t.Fatalf("%s: %q %v %v", raw, got, recognized, err)
		}
	}
	for _, raw := range []string{"https://youtube.com/playlist?list=abc", "https://youtu.be/bad", "https://youtube.com/watch?v=3KtWfp0UopM&v=abcdefghijk", "https://youtube.com:444/watch?v=3KtWfp0UopM"} {
		if _, recognized, err := YouTubeURL(raw); !recognized || err == nil {
			t.Errorf("unsupported YouTube URL accepted: %s", raw)
		}
	}
	for _, raw := range []string{"https://youtube.com.evil.example/watch?v=3KtWfp0UopM", "https://example.com/youtube.com"} {
		if _, recognized, _ := YouTubeURL(raw); recognized {
			t.Errorf("untrusted host recognized: %s", raw)
		}
	}
}

func TestGeminiSendsYouTubeAsVideo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Contents []struct {
				Parts []struct {
					FileData struct {
						FileURI  string `json:"fileUri"`
						MIMEType string `json:"mimeType"`
					} `json:"fileData"`
				} `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, c := range payload.Contents {
			for _, p := range c.Parts {
				if p.FileData.FileURI == "https://www.youtube.com/watch?v=3KtWfp0UopM" && p.FileData.MIMEType == "video/mp4" {
					found = true
				}
			}
		}
		if !found {
			t.Error("request must attach the video, not just describe its URL")
		}
		json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": `{"sufficient":true,"title":"動画の日本語タイトル","points":["動画の要点"],"tldr":["短い結論","重要な理由"]}`}}}}}})
	}))
	defer server.Close()
	g := &Gemini{Project: "test", Location: "global", Model: "test", TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"}), Endpoint: server.URL, Client: server.Client()}
	result, err := g.SummarizeVideo(context.Background(), "https://youtu.be/3KtWfp0UopM", summary.Detailed)
	if err != nil || len(result.Points) != 1 || len(result.TLDR) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
}
