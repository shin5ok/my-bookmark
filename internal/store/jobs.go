package store

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"cloud.google.com/go/firestore"
	"my-bookmark/internal/summary"
)

func readQuota(ctx context.Context, tx *firestore.Transaction, c *firestore.Client, uid string, now time.Time) (quota, *firestore.DocumentRef, error) {
	ref := c.Collection("quotas").Doc(uid)
	d, err := tx.Get(ref)
	if err != nil && !missing(err) {
		return quota{}, nil, err
	}
	var q quota
	if err == nil {
		if err = d.DataTo(&q); err != nil {
			return q, nil, err
		}
	}
	return q, ref, nil
}
func consumeQuota(q *quota, now time.Time) bool {
	if !q.Minute.Equal(now.Truncate(time.Minute)) {
		q.Minute = now.Truncate(time.Minute)
		q.MinuteCount = 0
	}
	if q.Day != now.Format("2006-01-02") {
		q.Day = now.Format("2006-01-02")
		q.DayCount = 0
	}
	if q.MinuteCount >= 3 || q.DayCount >= 50 {
		return false
	}
	q.MinuteCount++
	q.DayCount++
	q.ExpiresAt = now.Add(48 * time.Hour)
	return true
}
func (s *Store) Enqueue(ctx context.Context, uid, aid string, style summary.Style, instruction string) (string, error) {
	if !style.Valid() {
		return "", summary.ErrInvalidStyle
	}
	instruction = strings.TrimSpace(instruction)
	if utf8.RuneCountInString(instruction) > summary.MaxInstructionLength {
		return "", errors.New("追加指示は200文字以内で入力してください。")
	}
	now := time.Now().UTC()
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(s.bookmark(uid, aid)); err != nil {
			return normalizeError(err)
		}
		ar := s.article(aid)
		ad, err := tx.Get(ar)
		if err != nil {
			return normalizeError(err)
		}
		var a Article
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		if a.Status == "ready" && style == summary.Standard && instruction == "" {
			return ErrBusy
		}
		jr := s.client.Collection("summary_jobs").Doc(aid)
		jd, je := tx.Get(jr)
		if je != nil && !missing(je) {
			return je
		}
		var j SummaryJob
		if je == nil {
			if err = jd.DataTo(&j); err != nil {
				return err
			}
			if j.Status == "queued" || j.Status == "dispatched" || j.Status == "running" && j.LeaseUntil.After(now) {
				return ErrBusy
			}
		}
		q, qr, err := readQuota(ctx, tx, s.client, uid, now)
		if err != nil {
			return err
		}
		if !consumeQuota(&q, now) {
			return ErrRateLimited
		}
		j.ArticleID = aid
		j.Style = style
		j.Instruction = instruction
		j.UserID = uid
		j.Status = "queued"
		j.Stage = "queued"
		j.QueuedAt = now
		j.Lease = ""
		j.LeaseUntil = time.Time{}
		j.LastError = ""
		j.Attempts = 0
		a.Status = "queued"
		a.Stage = "queued"
		a.Progress = "再試行を受け付けました"
		if style != summary.Standard || instruction != "" {
			a.Progress = "ページを再取得して要約し直します"
		}
		a.LastError = ""
		a.Lease = ""
		a.LeaseUntil = time.Time{}
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		if err = tx.Set(jr, j); err != nil {
			return err
		}
		return tx.Set(qr, q)
	})
	if err != nil {
		return "", err
	}
	return "queued", nil
}

// QueuedJobs is the durable outbox shared by browser and API registrations.
func (s *Store) QueuedJobs(ctx context.Context) ([]SummaryJob, error) {
	docs, err := s.client.Collection("summary_jobs").Where("status", "==", "queued").OrderBy("queued_at", firestore.Asc).Limit(10).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	jobs := make([]SummaryJob, 0, len(docs))
	for _, d := range docs {
		var j SummaryJob
		if err := d.DataTo(&j); err != nil {
			return nil, err
		}
		j.ArticleID = d.Ref.ID
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *Store) MarkDispatched(ctx context.Context, id string, queuedAt time.Time) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ref := s.client.Collection("summary_jobs").Doc(id)
		d, err := tx.Get(ref)
		if missing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var j SummaryJob
		if err = d.DataTo(&j); err != nil {
			return err
		}
		// A task can start before CreateTask returns, or a new request can supersede it.
		if j.Status != "queued" || !j.QueuedAt.Equal(queuedAt) {
			return nil
		}
		return tx.Update(ref, []firestore.Update{{Path: "status", Value: "dispatched"}})
	})
}

// ClaimJob returns nil for obsolete/completed tasks, ErrBusy while another
// worker owns the lease, and reclaims work after a crashed worker's lease expires.
func (s *Store) ClaimJob(ctx context.Context, id string, queuedAt time.Time) (*SummaryJob, string, error) {
	token := RandomToken()
	var claimed *SummaryJob
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		claimed = nil // Firestore may rerun this closure after contention.
		jr := s.client.Collection("summary_jobs").Doc(id)
		jd, err := tx.Get(jr)
		if missing(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var j SummaryJob
		if err = jd.DataTo(&j); err != nil {
			return err
		}
		if !j.QueuedAt.Equal(queuedAt) {
			return nil
		}
		if j.Status != "queued" && j.Status != "dispatched" && j.Status != "running" {
			return nil
		}
		now := time.Now().UTC()
		if j.Status == "running" && j.LeaseUntil.After(now) {
			return ErrBusy
		}
		ar := s.article(id)
		ad, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var a Article
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		if !a.Active || a.Count <= 0 {
			j.Status = "cancelled"
			a.Status, a.Stage, a.Progress, a.Lease = "pending", "", "", ""
			a.LeaseUntil = time.Time{}
			j.Lease, j.LeaseUntil = "", time.Time{}
		} else {
			j.ArticleID = id
			j.Status, j.Stage = "running", "fetching"
			j.Attempts++
			j.Lease, j.LeaseUntil = token, now.Add(JobLeaseDuration)
			a.Status, a.Stage, a.Progress = "processing", "fetching", "要約対象を確認しています"
			a.Lease, a.LeaseUntil, a.LastError = token, j.LeaseUntil, ""
			claimed = &j
		}
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		return tx.Set(jr, j)
	})
	if err != nil {
		return nil, "", err
	}
	if claimed == nil {
		return nil, "", nil
	}
	return claimed, token, nil
}

const JobLeaseDuration = 11 * time.Minute

func (s *Store) NextQueued(ctx context.Context) (*SummaryJob, string, error) {
	jobs, err := s.QueuedJobs(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, j := range jobs {
		job, lease, err := s.ClaimJob(ctx, j.ArticleID, j.QueuedAt)
		if errors.Is(err, ErrBusy) {
			continue
		}
		if err != nil || job != nil {
			return job, lease, err
		}
	}
	return nil, "", nil
}

// RetryJob releases the lease for Cloud Tasks' next delivery. It deliberately
// does not put the job back in the outbox, since the existing task owns retries.
func (s *Store) RetryJob(ctx context.Context, id, lease string) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		jr, ar := s.client.Collection("summary_jobs").Doc(id), s.article(id)
		jd, err := tx.Get(jr)
		if err != nil {
			return err
		}
		ad, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var j SummaryJob
		var a Article
		if err = jd.DataTo(&j); err != nil {
			return err
		}
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		if j.Status != "running" || j.Lease != lease || a.Lease != lease {
			return ErrBusy
		}
		j.Status, j.Stage, j.Lease, j.LeaseUntil = "dispatched", "queued", "", time.Time{}
		a.Status, a.Stage, a.Progress = "queued", "queued", "一時的なエラーのため、時間をおいて再試行します"
		a.Lease, a.LeaseUntil = "", time.Time{}
		if err = tx.Set(jr, j); err != nil {
			return err
		}
		return tx.Set(ar, a)
	})
}
func (s *Store) JobOwner(ctx context.Context, id string) (string, error) {
	d, e := s.client.Collection("summary_jobs").Doc(id).Get(ctx)
	if e != nil {
		return "", normalizeError(e)
	}
	var j SummaryJob
	if e = d.DataTo(&j); e != nil {
		return "", e
	}
	return j.UserID, nil
}
func (s *Store) UpdateStage(ctx context.Context, id, lease, stage, progress string, sources []string) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ar := s.article(id)
		ad, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var a Article
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		jr := s.client.Collection("summary_jobs").Doc(id)
		jd, err := tx.Get(jr)
		if err != nil {
			return err
		}
		var j SummaryJob
		if err = jd.DataTo(&j); err != nil {
			return err
		}
		if a.Lease != lease || j.Lease != lease || j.Status != "running" {
			return ErrBusy
		}
		a.Stage = stage
		a.Progress = progress
		// Keep the references paired with the last successful summary until replacement.
		if len(a.Points) == 0 {
			a.Sources = sources
		}
		j.Stage = stage
		j.LeaseUntil = time.Now().Add(JobLeaseDuration)
		a.LeaseUntil = j.LeaseUntil
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		return tx.Set(jr, j)
	})
}
func (s *Store) FinishJob(ctx context.Context, id, lease, title string, points []string, model string, sources []string, failure string, tldr []string) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ar := s.article(id)
		ad, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var a Article
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		jr := s.client.Collection("summary_jobs").Doc(id)
		jd, err := tx.Get(jr)
		if err != nil {
			return err
		}
		var j SummaryJob
		if err = jd.DataTo(&j); err != nil {
			return err
		}
		if a.Lease != lease || j.Lease != lease || j.Status != "running" {
			return ErrBusy
		}
		a.Lease = ""
		a.LeaseUntil = time.Time{}
		j.Lease = ""
		j.LeaseUntil = time.Time{}
		a.Progress = ""
		j.LastError = failure
		a.LastError = failure
		if failure == "" || len(a.Points) == 0 {
			a.Sources = sources
			if title != "" {
				a.Title = title
			}
		}
		if failure == "" {
			a.Status = "ready"
			a.Stage = "ready"
			a.LastError = ""
			j.Status = "done"
			j.Stage = "done"
			a.Points = points
			a.TLDR = tldr
			a.Model = model
		} else {
			a.Status = "failed"
			a.Stage = "failed"
			j.Status = "failed"
			j.Stage = "failed"
		}
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		return tx.Set(jr, j)
	})
}
