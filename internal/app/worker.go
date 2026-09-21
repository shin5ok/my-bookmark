package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"my-bookmark/internal/content"
	"my-bookmark/internal/store"
)

type JobQueue interface {
	NextQueued(context.Context) (*store.SummaryJob, string, error)
	Get(context.Context, string) (store.Article, error)
	UpdateStage(context.Context, string, string, string, string, []string) error
	FinishJob(context.Context, string, string, string, []string, string, []string, string) error
}
type Worker struct {
	db       JobQueue
	summary  Summarizer
	fetcher  *http.Client
	renderer content.Renderer
	model    string
}

func NewWorker(db JobQueue, summary Summarizer, model string) *Worker {
	return &Worker{db: db, summary: summary, fetcher: content.NewFetcher(), renderer: content.NewChromeRenderer(), model: model}
}
func (w *Worker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, lease, err := w.db.NextQueued(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("summary queue scan failed", "error", err)
			if !wait(ctx, 2*time.Second) {
				return
			}
			continue
		}
		if job == nil {
			if !wait(ctx, 2*time.Second) {
				return
			}
			continue
		}
		w.process(ctx, job.ArticleID, lease)
	}
}
func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (w *Worker) process(parent context.Context, id, lease string) {
	ctx, cancel := context.WithTimeout(parent, 9*time.Minute)
	defer cancel()
	article, err := w.db.Get(ctx, id)
	var points []string
	title := ""
	var sources []string
	failure := ""
	if err != nil {
		failure = "記事を読み込めませんでした。"
	} else if !article.Active || article.Count == 0 {
		failure = "この記事はブックマークされていません。"
	} else {
		var doc content.Document
		doc, err = content.FetchDocument(ctx, w.fetcher, article.URL)
		if err != nil {
			failure = "記事を取得できませんでした。公開されたHTML記事か確認してください。"
		} else {
			title = doc.Title
			sources = []string{doc.URL}
			if len([]rune(doc.Text)) >= 1200 {
				if e := w.stage(ctx, id, lease, "summarizing", "記事の要点を確認しています", sources); e != nil {
					failure = "要約を中断しました。"
				} else {
					points, err = w.summary.Summarize(ctx, title, doc.Text)
					if err != nil {
						failure = "要約を作成できませんでした。"
					}
				}
			}
			if failure == "" && len(points) == 0 {
				if err = w.stage(ctx, id, lease, "rendering", "ページを描画して本文を確認しています", sources); err != nil {
					failure = "要約を中断しました。"
				} else {
					rendered, e := w.renderer.Render(ctx, doc.HTML, doc.URL)
					if e != nil {
						failure = "ページの描画に失敗しました。"
					} else {
						if rendered.Title != "" {
							title = rendered.Title
						}
						doc.Text = rendered.Text
						doc.Links = mergeLinks(doc.Links, rendered.Links, doc.URL)
					}
				}
				if failure == "" {
					var combined strings.Builder
					fmt.Fprintf(&combined, "【元記事】%s\n%s", doc.URL, doc.Text)
					links := selectLinks(doc.Links, doc.URL, 3)
					for i, link := range links {
						if ctx.Err() != nil {
							failure = "要約処理が中断されました。"
							break
						}
						if e := w.stage(ctx, id, lease, "following_links", fmt.Sprintf("関連ページを確認しています（%d/%d）", i+1, len(links)), append(append([]string(nil), sources...), link.URL)); e != nil {
							failure = "要約処理が中断されました。"
							break
						}
						childCtx, childCancel := context.WithTimeout(ctx, 12*time.Second)
						child, e := content.FetchDocument(childCtx, w.fetcher, link.URL)
						childCancel()
						if e != nil {
							continue
						}
						if len([]rune(child.Text)) < 160 {
							continue
						}
						sources = append(sources, child.URL)
						fmt.Fprintf(&combined, "\n\n【参考記事】%s\nタイトル: %s\n%s", child.URL, child.Title, child.Text)
					}
					if failure == "" {
						input := content.TruncateText(combined.String(), 24000)
						if e := w.stage(ctx, id, lease, "summarizing", "記事と関連ページから要点をまとめています", sources); e != nil {
							failure = "要約処理が中断されました。"
						} else {
							points, err = w.summary.Summarize(ctx, title, input)
							if err != nil {
								failure = "要約を作成できませんでした。"
							} else if err = content.ValidateSummary(points); err != nil {
								failure = "記事の情報が不足していて要約を作成できませんでした。"
							}
						}
					}
				}
			} else if failure == "" {
				if err = content.ValidateSummary(points); err != nil {
					failure = "記事の要約形式を確認できませんでした。"
				}
			}
		}
	}
	if ctx.Err() != nil && failure == "" {
		failure = "要約処理が中断されました。"
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer finishCancel()
	if e := w.db.FinishJob(finishCtx, id, lease, title, points, w.model, sources, failure); e != nil {
		if !errors.Is(e, store.ErrBusy) {
			slog.Error("summary result could not be saved", "article", id, "error", e)
		}
		return
	}
	if failure != "" {
		slog.Warn("summary job failed", "article", id, "stage", failure)
	}
}
func (w *Worker) stage(ctx context.Context, id, lease, stage, progress string, sources []string) error {
	return w.db.UpdateStage(ctx, id, lease, stage, progress, sources)
}
func mergeLinks(a, b []content.Link, base string) []content.Link {
	seen := map[string]bool{base: true}
	out := make([]content.Link, 0, len(a)+len(b))
	for _, link := range append(a, b...) {
		if link.URL == "" || seen[link.URL] {
			continue
		}
		seen[link.URL] = true
		out = append(out, link)
	}
	return out
}
func selectLinks(links []content.Link, base string, limit int) []content.Link {
	out := make([]content.Link, 0, limit)
	for _, link := range links {
		if link.URL == base {
			continue
		}
		out = append(out, link)
		if len(out) == limit {
			break
		}
	}
	return out
}
