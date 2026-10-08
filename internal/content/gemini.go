package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/oauth2"
	"my-bookmark/internal/summary"
)

type Gemini struct {
	Project, Location, Model string
	TokenSource              oauth2.TokenSource
	Client                   *http.Client
	Endpoint                 string
}

func (g *Gemini) Summarize(ctx context.Context, title, body string, style summary.Style) (summary.Result, error) {
	if !style.Valid() {
		return summary.Result{}, summary.ErrInvalidStyle
	}
	if g.Project == "" || g.Location == "" || g.Model == "" || g.TokenSource == nil {
		return summary.Result{}, errors.New("Gemini Vertex AI configuration is incomplete")
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"sufficient": map[string]any{"type": "boolean"},
		"title":      map[string]any{"type": "string"},
		"tldr":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 0, "maxItems": 5},
		"points":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 0, "maxItems": 5},
	}, "required": []string{"sufficient", "title", "points", "tldr"}}
	payload := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": SummaryInstruction(style)}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "記事タイトル: " + title + "\n記事本文:\n" + body}}}},
		"generationConfig":  map[string]any{"responseMimeType": "application/json", "responseJsonSchema": schema, "maxOutputTokens": 4096},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return summary.Result{}, err
	}
	endpoint := g.Endpoint
	if endpoint == "" {
		endpoint = "https://" + g.Location + "-aiplatform.googleapis.com"
		if g.Location == "global" {
			endpoint = "https://aiplatform.googleapis.com"
		}
	}
	path := "/v1/projects/" + url.PathEscape(g.Project) + "/locations/" + url.PathEscape(g.Location) + "/publishers/google/models/" + url.PathEscape(g.Model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+path, bytes.NewReader(data))
	if err != nil {
		return summary.Result{}, err
	}
	token, err := g.TokenSource.Token()
	if err != nil {
		return summary.Result{}, fmt.Errorf("get ADC access token: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return summary.Result{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return summary.Result{}, fmt.Errorf("Gemini returned HTTP %d", res.StatusCode)
	}
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text    string
					Thought bool
				}
			}
			FinishReason string
		}
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1024*1024)).Decode(&result); err != nil {
		return summary.Result{}, err
	}
	if len(result.Candidates) == 0 || result.Candidates[0].FinishReason != "STOP" {
		return summary.Result{}, errors.New("Gemini did not complete a summary")
	}
	var output strings.Builder
	for _, part := range result.Candidates[0].Content.Parts {
		if !part.Thought {
			output.WriteString(part.Text)
		}
	}
	var parsed struct {
		Sufficient bool     `json:"sufficient"`
		TLDR       []string `json:"tldr"`
		Title      string   `json:"title"`
		Points     []string `json:"points"`
	}
	if err := json.Unmarshal([]byte(output.String()), &parsed); err != nil {
		return summary.Result{}, err
	}
	for i := range parsed.Points {
		parsed.Points[i] = strings.TrimSpace(parsed.Points[i])
	}
	title = strings.TrimSpace(parsed.Title)
	if utf8.RuneCountInString(title) < 4 || utf8.RuneCountInString(title) > 60 || strings.ContainsAny(title, "\r\n<>") || !strings.ContainsFunc(title, func(r rune) bool {
		return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
	}) {
		title = ""
	}
	if !parsed.Sufficient {
		return summary.Result{Title: title}, nil
	}
	if err := ValidateSummary(parsed.Points); err != nil {
		return summary.Result{}, err
	}

	for i := range parsed.TLDR {
		parsed.TLDR[i] = strings.TrimSpace(parsed.TLDR[i])
	}
	if err := ValidateTLDR(parsed.TLDR); err != nil {
		return summary.Result{}, err
	}
	return summary.Result{Title: title, Points: parsed.Points, TLDR: parsed.TLDR}, nil
}

// ValidateTLDR keeps each semantic line short; layout may wrap on narrow screens.
func ValidateTLDR(lines []string) error {
	if len(lines) < 2 || len(lines) > 5 {
		return errors.New("TLDR must contain 2 to 5 sentences")
	}
	seen := make(map[string]bool)
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || utf8.RuneCountInString(line) > 100 || strings.ContainsAny(line, "\r\n") || seen[line] {
			return errors.New("invalid TLDR sentence")
		}
		seen[line] = true
	}
	return nil
}
