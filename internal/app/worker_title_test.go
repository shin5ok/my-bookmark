package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
)

type titleJobQueue struct {
	article store.Article
	title   string
	failure string
	tldr    []string
}

func (q *titleJobQueue) NextQueued(context.Context) (*store.SummaryJob, string, error) {
	return nil, "", nil
}
func (q *titleJobQueue) Get(context.Context, string) (store.Article, error) { return q.article, nil }
func (q *titleJobQueue) UpdateStage(context.Context, string, string, string, string, []string) error {
	return nil
}
func (q *titleJobQueue) FinishJob(_ context.Context, _, _, title string, _ []string, _ string, _ []string, failure string, tldr []string) error {
	q.title, q.failure, q.tldr = title, failure, tldr
	return nil
}

type titleSummarizer struct{ result summary.Result }

func (s titleSummarizer) Summarize(context.Context, string, string, summary.Style, string) (summary.Result, error) {
	return s.result, nil
}

func TestWorkerUsesGeneratedTitleForInitialAndRestyledSummary(t *testing.T) {
	for _, tc := range []struct {
		style           summary.Style
		generated, want string
	}{
		{summary.Concrete, "新しい日本語タイトル", "新しい日本語タイトル"},
		{summary.Concrete, "", "以前のタイトル"},
		{summary.Standard, "新しい日本語タイトル", "新しい日本語タイトル"},
		{summary.Standard, "", "元の記事タイトル"},
	} {
		t.Run(string(tc.style)+tc.generated, func(t *testing.T) {
			q := &titleJobQueue{article: store.Article{URL: "https://example.com/article", Title: "以前のタイトル", Active: true, Count: 1}}
			w := NewWorker(q, titleSummarizer{summary.Result{Title: tc.generated, TLDR: []string{"結論", "重要性", "影響"}, Points: []string{"記事の結論"}}}, "test")
			w.fetcher = &http.Client{Transport: articleTransportWithTitle{}}
			w.process(context.Background(), "article", "lease", tc.style, "")
			if len(q.tldr) != 3 {
				t.Fatal("TLDR was not forwarded")
			}
			if q.failure != "" || q.title != tc.want {
				t.Fatalf("title=%q failure=%q; want %q", q.title, q.failure, tc.want)
			}
		})
	}
}

type articleTransportWithTitle struct{}

func (articleTransportWithTitle) RoundTrip(*http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteString(`<html><title>元の記事タイトル</title><article><p>` + strings.Repeat("記事の本文です。", 200) + `</p></article></html>`)
	return w.Result(), nil
}
