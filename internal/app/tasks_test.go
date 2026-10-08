package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	"my-bookmark/internal/content"
	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
)

func TestCloudTasksPublishesAuthenticatedIdempotentTask(t *testing.T) {
	var names []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/projects/test/locations/asia-northeast1/queues/summary/tasks" || r.Header.Get("Authorization") != "Bearer adc" {
			t.Error("incorrect endpoint or ADC authentication")
		}
		var request struct {
			Task struct {
				Name, DispatchDeadline string
				HTTPRequest            struct {
					HTTPMethod, URL, Body string
					OIDCToken             struct{ ServiceAccountEmail, Audience string }
				}
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		task := request.Task
		names = append(names, task.Name)
		if task.HTTPRequest.URL != "https://worker.run.app/tasks/summary" || task.HTTPRequest.HTTPMethod != "POST" || task.DispatchDeadline != "600s" {
			t.Errorf("bad HTTP task: %+v", task)
		}
		if task.HTTPRequest.OIDCToken.ServiceAccountEmail != "tasks@test.iam.gserviceaccount.com" || task.HTTPRequest.OIDCToken.Audience != "https://worker.run.app" {
			t.Error("missing invocation identity")
		}
		body, err := base64.StdEncoding.DecodeString(task.HTTPRequest.Body)
		var input TaskRequest
		if err != nil || json.Unmarshal(body, &input) != nil || input.ArticleID != strings.Repeat("a", 64) || input.QueuedAt.IsZero() {
			t.Errorf("invalid task body: %s", body)
		}
		if len(names) == 2 {
			w.WriteHeader(409)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer server.Close()
	publisher := &CloudTasks{Queue: "projects/test/locations/asia-northeast1/queues/summary", WorkerURL: "https://worker.run.app", ServiceAccount: "tasks@test.iam.gserviceaccount.com", Endpoint: server.URL, Client: server.Client(), TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "adc"})}
	job := store.SummaryJob{ArticleID: strings.Repeat("a", 64), QueuedAt: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)}
	for i := 0; i < 3; i++ {
		if i == 2 {
			job.QueuedAt = job.QueuedAt.Add(time.Second)
		}
		if err := publisher.Publish(context.Background(), job); err != nil {
			t.Fatal(err)
		}
	}
	if names[0] != names[1] || names[0] == names[2] {
		t.Fatalf("task identities: %v", names)
	}
}

type outboxFixture struct{ marked bool }

func (f *outboxFixture) QueuedJobs(context.Context) ([]store.SummaryJob, error) {
	return []store.SummaryJob{{ArticleID: "article", QueuedAt: time.Now()}}, nil
}
func (f *outboxFixture) MarkDispatched(context.Context, string, time.Time) error {
	f.marked = true
	return nil
}

type failedPublisher struct{}

func (failedPublisher) Publish(context.Context, store.SummaryJob) error {
	return errors.New("unavailable")
}

func TestDispatchFailureLeavesOutboxPending(t *testing.T) {
	f := &outboxFixture{}
	if err := DispatchJobs(context.Background(), f, failedPublisher{}); err == nil || f.marked {
		t.Fatalf("failed task was removed from outbox: %+v %v", f, err)
	}
}

type videoSummaryFixture struct {
	err   error
	style summary.Style
}

func (s *videoSummaryFixture) Summarize(context.Context, string, string, summary.Style) (summary.Result, error) {
	return summary.Result{}, errors.New("video incorrectly sent through article path")
}
func (s *videoSummaryFixture) SummarizeVideo(_ context.Context, _ string, style summary.Style) (summary.Result, error) {
	s.style = style
	return summary.Result{Title: "動画の日本語タイトル", Points: []string{"動画の結論"}, TLDR: []string{"短い結論", "理由"}}, s.err
}

type taskStoreFixture struct {
	titleJobQueue
	job                 *store.SummaryJob
	claimErr, finishErr error
	retried             bool
}

func (s *taskStoreFixture) ClaimJob(context.Context, string, time.Time) (*store.SummaryJob, string, error) {
	return s.job, "lease", s.claimErr
}
func (s *taskStoreFixture) RetryJob(context.Context, string, string) error {
	s.retried = true
	return nil
}
func (s *taskStoreFixture) FinishJob(ctx context.Context, id, lease, title string, points []string, model string, sources []string, failure string, tldr []string) error {
	if s.finishErr != nil {
		return s.finishErr
	}
	return s.titleJobQueue.FinishJob(ctx, id, lease, title, points, model, sources, failure, tldr)
}

func TestSummaryTaskDelivery(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		attempt                     int
		apiErr, claimErr, finishErr error
		stale                       bool
		status                      int
		retry                       bool
	}{
		{name: "success", attempt: 1, status: 204},
		{name: "transient API error", attempt: 1, apiErr: &content.APIError{Status: 429}, status: 503, retry: true},
		{name: "retry exhausted", attempt: 3, apiErr: &content.APIError{Status: 503}, status: 204},
		{name: "unavailable video", attempt: 1, apiErr: &content.APIError{Status: 400}, status: 204},
		{name: "active duplicate", claimErr: store.ErrBusy, status: 503},
		{name: "stale or finished", stale: true, status: 204},
		{name: "result save failed", attempt: 1, finishErr: errors.New("storage unavailable"), status: 503, retry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := strings.Repeat("a", 64)
			db := &taskStoreFixture{titleJobQueue: titleJobQueue{article: store.Article{URL: "https://youtu.be/3KtWfp0UopM", Active: true, Count: 1}}, job: &store.SummaryJob{ArticleID: id, Attempts: tc.attempt, Style: summary.Detailed}, claimErr: tc.claimErr, finishErr: tc.finishErr}
			if tc.stale {
				db.job = nil
			}
			summarizer := &videoSummaryFixture{err: tc.apiErr}
			worker := NewWorker(db, summarizer, "test")
			// A video task must not require an HTTP fetcher or Chromium.
			worker.fetcher, worker.renderer = nil, nil
			h := TaskHandler(db, worker)
			r := httptest.NewRequest("POST", "/tasks/summary", strings.NewReader(`{"article_id":"`+id+`","queued_at":"2026-10-08T00:00:00Z"}`))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || db.retried != tc.retry {
				t.Fatalf("HTTP %d retry=%v body=%s", w.Code, db.retried, w.Body.String())
			}
			if tc.name == "success" && (db.title != "動画の日本語タイトル" || len(db.tldr) != 2 || summarizer.style != summary.Detailed) {
				t.Fatalf("video result not saved: %+v", db)
			}
			if (tc.name == "retry exhausted" || tc.name == "unavailable video") && db.failure == "" {
				t.Fatal("terminal failure not visible")
			}
		})
	}
}

func TestTaskHandlerRejectsInvalidInput(t *testing.T) {
	h := TaskHandler(nil, nil)
	for _, body := range []string{`{}`, `{"article_id":"../bad","queued_at":"2026-10-08T00:00:00Z"}`, `{"article_id":"` + strings.Repeat("a", 64) + `","queued_at":"2026-10-08T00:00:00Z"} {}`} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/tasks/summary", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("accepted invalid task: %s", body)
		}
	}
}
