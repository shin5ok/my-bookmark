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
)

type Gemini struct {
	Key, Model string
	Client     *http.Client
	Endpoint   string
}

func (g *Gemini) Summarize(ctx context.Context, title, body string) ([]string, error) {
	if g.Key == "" {
		return nil, errors.New("Gemini API key is not configured")
	}
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"sufficient": map[string]any{"type": "boolean"},
		"points":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 0, "maxItems": 5},
	}, "required": []string{"sufficient", "points"}}
	payload := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]string{"text": SummaryInstruction()}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]string{"text": "記事タイトル: " + title + "\n記事本文:\n" + body}}}},
		"generationConfig":  map[string]any{"responseMimeType": "application/json", "responseJsonSchema": schema, "maxOutputTokens": 4096},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := g.Endpoint
	if endpoint == "" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint+"/models/"+url.PathEscape(g.Model)+":generateContent", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.Key)
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Gemini returned HTTP %d", res.StatusCode)
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
		return nil, err
	}
	if len(result.Candidates) == 0 || result.Candidates[0].FinishReason != "STOP" {
		return nil, errors.New("Gemini did not complete a summary")
	}
	var output strings.Builder
	for _, part := range result.Candidates[0].Content.Parts {
		if !part.Thought {
			output.WriteString(part.Text)
		}
	}
	var summary struct {
		Sufficient bool     `json:"sufficient"`
		Points     []string `json:"points"`
	}
	if err := json.Unmarshal([]byte(output.String()), &summary); err != nil {
		return nil, err
	}
	for i := range summary.Points {
		summary.Points[i] = strings.TrimSpace(summary.Points[i])
	}
	if !summary.Sufficient {
		return nil, nil
	}
	if err := ValidateSummary(summary.Points); err != nil {
		return nil, err
	}
	return summary.Points, nil
}
