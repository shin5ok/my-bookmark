package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"my-bookmark/internal/store"
)

type TaskRequest struct {
	ArticleID string    `json:"article_id"`
	QueuedAt  time.Time `json:"queued_at"`
}

type TaskPublisher interface {
	Publish(context.Context, store.SummaryJob) error
}

// CloudTasks uses the REST API with ADC, as the Gemini client does.
type CloudTasks struct {
	Queue, WorkerURL, ServiceAccount string
	Client                           *http.Client
	TokenSource                      oauth2.TokenSource
	Endpoint                         string
}

func (c *CloudTasks) Publish(ctx context.Context, job store.SummaryJob) error {
	body, err := json.Marshal(TaskRequest{ArticleID: job.ArticleID, QueuedAt: job.QueuedAt})
	if err != nil {
		return err
	}
	// Stable across dispatcher crashes; distinct for every user-requested generation.
	name := c.Queue + "/tasks/" + store.Hash(job.ArticleID+":"+job.QueuedAt.UTC().Format(time.RFC3339Nano))
	payload, err := json.Marshal(map[string]any{"task": map[string]any{
		"name": name, "dispatchDeadline": "600s",
		"httpRequest": map[string]any{
			"httpMethod": "POST", "url": c.WorkerURL + "/tasks/summary",
			"headers":   map[string]string{"Content-Type": "application/json"},
			"body":      base64.StdEncoding.EncodeToString(body),
			"oidcToken": map[string]string{"serviceAccountEmail": c.ServiceAccount, "audience": c.WorkerURL},
		},
	}})
	if err != nil {
		return err
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = "https://cloudtasks.googleapis.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v2/"+c.Queue+"/tasks", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if c.TokenSource == nil {
		return errors.New("Cloud Tasks ADC is not configured")
	}
	token, err := c.TokenSource.Token()
	if err != nil {
		return fmt.Errorf("get Cloud Tasks ADC token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	if res.StatusCode == http.StatusConflict || res.StatusCode >= 200 && res.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("Cloud Tasks returned HTTP %d", res.StatusCode)
}

type DispatchStore interface {
	QueuedJobs(context.Context) ([]store.SummaryJob, error)
	MarkDispatched(context.Context, string, time.Time) error
}

func DispatchJobs(ctx context.Context, db DispatchStore, publisher TaskPublisher) error {
	jobs, err := db.QueuedJobs(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, job := range jobs {
		if err := publisher.Publish(ctx, job); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := db.MarkDispatched(ctx, job.ArticleID, job.QueuedAt); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func RunDispatcher(ctx context.Context, db DispatchStore, publisher TaskPublisher) {
	for ctx.Err() == nil {
		if err := DispatchJobs(ctx, db, publisher); err != nil {
			slog.Error("task dispatch failed; outbox will be retried", "error", err)
		}
		if !wait(ctx, 2*time.Second) {
			return
		}
	}
}

type TaskStore interface {
	JobQueue
	ClaimJob(context.Context, string, time.Time) (*store.SummaryJob, string, error)
	RetryJob(context.Context, string, string) error
}

// TaskHandler runs only in a dedicated, IAM-authenticated Cloud Run service.
// Cloud Run validates the Cloud Tasks OIDC token before forwarding requests.
// Neither the IAP UI service nor the public token API registers these routes.
func TaskHandler(db TaskStore, worker *Worker) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("POST /tasks/summary", func(w http.ResponseWriter, r *http.Request) {
		var input TaskRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || !validID.MatchString(input.ArticleID) || input.QueuedAt.IsZero() {
			http.Error(w, "invalid task", http.StatusBadRequest)
			return
		}
		if decoder.Decode(new(any)) != io.EOF {
			http.Error(w, "invalid task", http.StatusBadRequest)
			return
		}
		job, lease, err := db.ClaimJob(r.Context(), input.ArticleID, input.QueuedAt)
		if err != nil {
			http.Error(w, "job claim unavailable", http.StatusServiceUnavailable)
			return
		}
		if job == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		err = worker.processAttempt(r.Context(), job.ArticleID, lease, job.Style, job.Instruction, job.Attempts < 3)
		if err != nil && !errors.Is(err, store.ErrBusy) {
			// Release the lease even when the HTTP request's context has expired.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
			defer cancel()
			if retryErr := db.RetryJob(ctx, job.ArticleID, lease); retryErr != nil && !errors.Is(retryErr, store.ErrBusy) {
				slog.Error("task lease release failed", "article", job.ArticleID, "error", retryErr)
			}
			slog.Warn("summary task will retry", "article", job.ArticleID, "error", err)
			http.Error(w, "summary temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}
