package content

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
	"my-bookmark/internal/summary"
)

func TestGeminiImageSelectionBounds(t *testing.T) {
	for _, tc := range []struct {
		output  string
		want    int
		wantErr bool
	}{
		{`{"images":[1]}`, 1, false}, {`{"images":[]}`, 0, false},
		{`{"images":[0,1,2]}`, 3, false}, {`{"images":[0,1,2,3]}`, 0, true},
		{`{"images":[99]}`, 0, true}, {`{"images":[0,0]}`, 0, true},
	} {
		t.Run(tc.output, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeGeminiImagesResponse(w, tc.output) }))
			defer server.Close()
			g := imageTestGemini(server)
			selected, err := g.SelectImages(context.Background(), "title", "body", []ImageCandidate{{URL: "https://example.com/0"}, {URL: "https://example.com/1"}, {URL: "https://example.com/2"}, {URL: "https://example.com/3"}}, 3)
			if (err != nil) != tc.wantErr || len(selected) != tc.want {
				t.Fatalf("selected=%+v err=%v", selected, err)
			}
			if tc.output == `{"images":[1]}` && selected[0].URL != "https://example.com/1" {
				t.Fatal("wrong candidate selected")
			}
		})
	}
}

func TestGeminiReceivesInlineImages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Contents []struct {
				Parts []struct {
					Text       string
					InlineData struct {
						MIMEType string `json:"mimeType"`
						Data     string
					}
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Contents) != 1 || len(payload.Contents[0].Parts) != 3 {
			t.Fatalf("parts: %+v", payload)
		}
		part := payload.Contents[0].Parts[2].InlineData
		decoded, err := base64.StdEncoding.DecodeString(part.Data)
		if err != nil || string(decoded) != "image-data" || part.MIMEType != "image/png" {
			t.Fatalf("inline data=%+v err=%v", part, err)
		}
		writeGeminiImagesResponse(w, `{"sufficient":true,"title":"グラフの要約結果","points":["売上が増加した"],"tldr":["売上が増加した","前年比で改善した"]}`)
	}))
	defer server.Close()
	g := imageTestGemini(server)
	result, err := g.SummarizeWithImages(context.Background(), "title", "body", []Image{{ImageCandidate: ImageCandidate{URL: "https://example.com/chart.png", Description: "売上グラフ"}, MIMEType: "image/png", Data: []byte("image-data")}}, summary.Standard, "")
	if err != nil || len(result.Points) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	_, err = g.SummarizeWithImages(context.Background(), "title", "body", make([]Image, 4), summary.Standard, "")
	if err == nil {
		t.Fatal("more than three images accepted")
	}
}

func imageTestGemini(s *httptest.Server) *Gemini {
	return &Gemini{Project: "test", Location: "global", Model: "test", Endpoint: s.URL, Client: s.Client(), TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})}
}
func writeGeminiImagesResponse(w http.ResponseWriter, output string) {
	json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": output}}}}}})
}
