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

func TestCustomInstructionAcrossSummaryInputs(t *testing.T) {
	for _, kind := range []string{"article", "images", "video"} {
		t.Run(kind, func(t *testing.T) {
			custom := "英語で7項目、TLDRは1文。記事本文という語はそのまま"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					SystemInstruction struct{ Parts []struct{ Text string } }
					GenerationConfig  struct{ ResponseJsonSchema map[string]any }
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatal(err)
				}
				prompt := request.SystemInstruction.Parts[0].Text
				if !strings.Contains(prompt, custom) || !strings.Contains(prompt, "追加指示を優先") || !strings.Contains(prompt, "入力資料の事実") {
					t.Errorf("missing literal instruction or priority: %s", prompt)
				}
				properties := request.GenerationConfig.ResponseJsonSchema["properties"].(map[string]any)
				for _, field := range []string{"points", "tldr"} {
					if _, capped := properties[field].(map[string]any)["maxItems"]; capped {
						t.Errorf("%s still capped", field)
					}
				}
				output := `{"sufficient":true,"title":"English title","points":["one","two","three","four","five","six","seven"],"tldr":["One sentence."]}`
				json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": output}}}}}})
			}))
			defer server.Close()
			g := &Gemini{Project: "test", Location: "global", Model: "test", Endpoint: server.URL, Client: server.Client(), TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test"})}
			var result summary.Result
			var err error
			switch kind {
			case "article":
				result, err = g.Summarize(context.Background(), "title", "body", summary.Simple, custom)
			case "images":
				result, err = g.SummarizeWithImages(context.Background(), "title", "body", []Image{{MIMEType: "image/png", Data: []byte("image")}}, summary.Simple, custom)
			case "video":
				result, err = g.SummarizeVideo(context.Background(), "https://youtu.be/3KtWfp0UopM", summary.Simple, custom)
			}
			if err != nil || result.Title != "English title" || len(result.Points) != 7 || len(result.TLDR) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestCustomInstructionKeepsDisplayableResults(t *testing.T) {
	for _, points := range [][]string{nil, {" "}} {
		if ValidateSummaryWithInstruction(points, "詳しく") == nil {
			t.Fatalf("accepted invalid points %v", points)
		}
	}
	for _, lines := range [][]string{nil, {" "}, {"one\ntwo"}, {"one", "one"}} {
		if ValidateTLDRWithInstruction(lines, "詳しく") == nil {
			t.Fatalf("accepted invalid TLDR %v", lines)
		}
	}
	if err := ValidateSummaryWithInstruction([]string{strings.Repeat("詳", 300)}, "詳しく"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTLDRWithInstruction([]string{strings.Repeat("詳", 120)}, "詳しく"); err != nil {
		t.Fatal(err)
	}
}
