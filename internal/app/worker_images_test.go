package app

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"my-bookmark/internal/content"
	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
)

type imageWorkerSummary struct {
	images         []content.Image
	renderFallback bool
	calls          int
}

func (s *imageWorkerSummary) Summarize(context.Context, string, string, summary.Style) (summary.Result, error) {
	return summary.Result{Title: "本文だけの要約結果", Points: []string{"本文の結論"}, TLDR: []string{"結論", "理由"}}, nil
}
func (s *imageWorkerSummary) SelectImages(_ context.Context, _, _ string, candidates []content.ImageCandidate, limit int) ([]content.ImageCandidate, error) {
	// Deliberately return too many: the worker must enforce its own download budget.
	return candidates, nil
}
func (s *imageWorkerSummary) SummarizeWithImages(_ context.Context, _, _ string, images []content.Image, _ summary.Style) (summary.Result, error) {
	s.calls++
	s.images = images
	if s.renderFallback && s.calls == 1 {
		return summary.Result{}, nil
	}
	return summary.Result{Title: "画像を含めた要約結果", Points: []string{"画像の売上が増加した"}, TLDR: []string{"画像から分かる結論", "増加した理由"}}, nil
}

type imageWorkerRenderer struct{}

func (imageWorkerRenderer) Render(context.Context, []byte, string) (content.Document, error) {
	return content.Document{Text: strings.Repeat("描画した本文。", 200), HTML: []byte(`<article><img src="/new.png" alt="比較図"></article>`)}, nil
}

type imageWorkerTransport struct {
	badImages bool
	fetched   int
}

func (tr *imageWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	if strings.HasSuffix(r.URL.Path, ".png") {
		tr.fetched++
		if tr.badImages {
			w.WriteHeader(403)
		} else {
			png.Encode(w, image.NewRGBA(image.Rect(0, 0, 320, 200)))
		}
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		var html bytes.Buffer
		html.WriteString(`<article>` + strings.Repeat("記事の本文です。", 200))
		for _, name := range []string{"a", "b", "c", "d"} {
			html.WriteString(`<figure><img src="/` + name + `.png" alt="売上グラフ"></figure>`)
		}
		html.WriteString(`</article>`)
		w.Write(html.Bytes())
	}
	return w.Result(), nil
}

func TestWorkerImageBudgetAndTextFallback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		bad, render bool
		wantTitle   string
	}{
		{"images", false, false, "画像を含めた要約結果"},
		{"failed downloads", true, false, "本文だけの要約結果"},
		{"render fallback shares budget", false, true, "画像を含めた要約結果"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &titleJobQueue{article: store.Article{URL: "https://example.com/article", Active: true, Count: 1}}
			s := &imageWorkerSummary{renderFallback: tc.render}
			tr := &imageWorkerTransport{badImages: tc.bad}
			w := NewWorker(q, s, "test")
			w.fetcher = &http.Client{Transport: tr}
			w.renderer = imageWorkerRenderer{}
			w.process(context.Background(), "article", "lease", summary.Standard)
			if q.failure != "" || q.title != tc.wantTitle {
				t.Fatalf("title=%s failure=%s", q.title, q.failure)
			}
			if tr.fetched != 3 {
				t.Fatalf("downloaded %d images; budget should be three", tr.fetched)
			}
			if !tc.bad && len(s.images) != 3 {
				t.Fatalf("summary images=%d", len(s.images))
			}
		})
	}
}
