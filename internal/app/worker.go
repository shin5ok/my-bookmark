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
	"my-bookmark/internal/summary"
)

type JobQueue interface {
	NextQueued(context.Context) (*store.SummaryJob, string, error)
	Get(context.Context, string) (store.Article, error)
	UpdateStage(context.Context, string, string, string, string, []string) error
	FinishJob(context.Context, string, string, string, []string, string, []string, string, []string) error
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
		w.process(ctx, job.ArticleID, lease, job.Style)
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
func (w *Worker) process(parent context.Context, id, lease string, style summary.Style) {
	if err := w.processAttempt(parent, id, lease, style, false); err != nil {
		slog.Error("summary processing failed", "article", id, "error", err)
	}
}

func (w *Worker) processAttempt(parent context.Context, id, lease string, style summary.Style, retryTransient bool) error {
	ctx, cancel := context.WithTimeout(parent, 9*time.Minute)
	defer cancel()
	article, err := w.db.Get(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var points []string
	var tldr []string
	title := ""
	generatedTitle := ""
	var sources []string
	failure := ""
	if err != nil {
		failure = "記事を読み込めませんでした。"
	} else if !article.Active || article.Count == 0 {
		failure = "この記事はブックマークされていません。"
	} else if videoURL, isVideo, videoErr := content.YouTubeURL(article.URL); isVideo {
		sources = []string{article.URL}
		if videoErr != nil {
			failure = videoErr.Error()
		} else {
			sources = []string{videoURL}
			video, supported := w.summary.(interface {
				SummarizeVideo(context.Context, string, summary.Style) (summary.Result, error)
			})
			if !supported {
				failure = "動画の要約が設定されていません。"
			} else if err = w.stage(ctx, id, lease, "summarizing", "動画の映像と音声から要点をまとめています", sources); err != nil {
				return err
			} else {
				var result summary.Result
				result, err = video.SummarizeVideo(ctx, videoURL, style)
				points, generatedTitle, tldr = result.Points, result.Title, result.TLDR
				if err != nil {
					failure = "動画を要約できませんでした。公開状態・視聴制限を確認し、時間をおいて再試行してください。"
				} else if content.ValidateSummary(points) != nil || content.ValidateTLDR(tldr) != nil {
					failure = "動画の内容を十分に確認できず、要約を作成できませんでした。"
				}
			}
		}
	} else {
		var doc content.Document
		doc, err = content.FetchDocument(ctx, w.fetcher, article.URL)
		if err != nil {
			failure = "記事を取得できませんでした。公開されたHTML記事か確認してください。"
		} else {
			title = doc.Title
			sources = []string{doc.URL}
			var images []content.Image
			attemptedImages := map[string]bool{}
			if e := w.collectImages(ctx, id, lease, doc, &images, attemptedImages, &sources); e != nil {
				return e
			}
			if len([]rune(doc.Text)) >= 1200 || len(images) > 0 {
				if e := w.stage(ctx, id, lease, "summarizing", "記事の要点を確認しています", sources); e != nil {
					return e
				} else {
					var result summary.Result
					result, err = w.summarizeArticle(ctx, title, doc.Text, images, style)
					points, generatedTitle, tldr = result.Points, result.Title, result.TLDR
					if err != nil {
						failure = "要約を作成できませんでした。"
					}
				}
			}
			if failure == "" && len(points) == 0 {
				if err = w.stage(ctx, id, lease, "rendering", "ページを描画して本文を確認しています", sources); err != nil {
					return err
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
						if len(rendered.HTML) > 0 {
							doc.HTML = rendered.HTML
							doc.ContentType = "text/html; charset=utf-8"
						}
						if e := w.collectImages(ctx, id, lease, doc, &images, attemptedImages, &sources); e != nil {
							return e
						}
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
							return e
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
							return e
						} else {
							var result summary.Result
							result, err = w.summarizeArticle(ctx, title, input, images, style)
							points, generatedTitle, tldr = result.Points, result.Title, result.TLDR
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
	if retryTransient && (content.Retryable(err) || ctx.Err() != nil) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	if ctx.Err() != nil && failure == "" {
		failure = "要約処理が中断されました。"
	}
	if generatedTitle != "" {
		title = generatedTitle
	} else if style != summary.Standard && article.Title != "" {
		title = article.Title
	}
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(parent), 3*time.Second)
	defer finishCancel()
	if e := w.db.FinishJob(finishCtx, id, lease, title, points, w.model, sources, failure, tldr); e != nil {
		if !errors.Is(e, store.ErrBusy) {
			slog.Error("summary result could not be saved", "article", id, "error", e)
		}
		return e
	}
	if failure != "" {
		slog.Warn("summary job failed", "article", id, "stage", failure)
	}
	return nil
}

type imageSummarizer interface {
	SelectImages(context.Context, string, string, []content.ImageCandidate, int) ([]content.ImageCandidate, error)
	SummarizeWithImages(context.Context, string, string, []content.Image, summary.Style) (summary.Result, error)
}

// collectImages shares a download budget across the initial and rendered page.
// Optional image failures leave text summarization available; job-stage errors
// still propagate so a lost lease never continues processing.
func (w *Worker) collectImages(ctx context.Context, id, lease string, doc content.Document, images *[]content.Image, attempted map[string]bool, sources *[]string) error {
	model, supported := w.summary.(imageSummarizer)
	remaining := content.MaxSummaryImages - len(attempted)
	if !supported || remaining <= 0 {
		return nil
	}
	var candidates []content.ImageCandidate
	allowed := map[string]content.ImageCandidate{}
	for _, candidate := range content.DocumentImages(doc.HTML, doc.ContentType, doc.URL) {
		if !attempted[candidate.URL] {
			candidates = append(candidates, candidate)
			allowed[candidate.URL] = candidate
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	if err := w.stage(ctx, id, lease, "selecting_images", "要約に役立つ図表・説明画像を選んでいます", *sources); err != nil {
		return err
	}
	selected, err := model.SelectImages(ctx, doc.Title, doc.Text, candidates, remaining)
	if err != nil {
		slog.Warn("image selection skipped", "article", id, "error", err)
		return nil
	}
	for _, selection := range selected {
		candidate, valid := allowed[selection.URL]
		if !valid || attempted[candidate.URL] || len(attempted) >= content.MaxSummaryImages {
			continue
		}
		attempted[candidate.URL] = true
		if err := w.stage(ctx, id, lease, "fetching_images", fmt.Sprintf("重要な画像を取得しています（%d/%d）", len(attempted), content.MaxSummaryImages), *sources); err != nil {
			return err
		}
		img, err := content.FetchImage(ctx, w.fetcher, candidate)
		if err != nil {
			slog.Warn("article image skipped", "article", id, "error", err)
			continue
		}
		*images = append(*images, img)
		*sources = append(*sources, candidate.URL)
	}
	return nil
}

func (w *Worker) summarizeArticle(ctx context.Context, title, body string, images []content.Image, style summary.Style) (summary.Result, error) {
	if model, supported := w.summary.(imageSummarizer); supported && len(images) > 0 {
		return model.SummarizeWithImages(ctx, title, body, images, style)
	}
	return w.summary.Summarize(ctx, title, body, style)
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
