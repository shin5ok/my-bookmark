package store

import (
	"context"
	"errors"
	"time"

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
func (s *Store) Enqueue(ctx context.Context, uid, aid string, style summary.Style) (string, error) {
	if !style.Valid() {
		return "", summary.ErrInvalidStyle
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
		if a.Status == "ready" && style == summary.Standard {
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
			if j.Status == "queued" || j.Status == "running" && j.LeaseUntil.After(now) {
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
		j.UserID = uid
		j.Status = "queued"
		j.Stage = "queued"
		j.QueuedAt = now
		j.Lease = ""
		j.LeaseUntil = time.Time{}
		j.LastError = ""
		a.Status = "queued"
		a.Stage = "queued"
		a.Progress = "再試行を受け付けました"
		if style != summary.Standard {
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
func (s *Store) NextQueued(ctx context.Context) (*SummaryJob, string, error) {
	docs, err := s.client.Collection("summary_jobs").Where("status", "==", "queued").OrderBy("queued_at", firestore.Asc).Limit(10).Documents(ctx).GetAll()
	if err != nil {
		return nil, "", err
	}
	for _, candidate := range docs {
		id := candidate.Ref.ID
		token := RandomToken()
		now := time.Now().UTC()
		var claimedJob SummaryJob
		claimed := false
		err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			jr := s.client.Collection("summary_jobs").Doc(id)
			jd, e := tx.Get(jr)
			if e != nil {
				return normalizeError(e)
			}
			var j SummaryJob
			if e = jd.DataTo(&j); e != nil {
				return e
			}
			if j.Status != "queued" {
				return ErrBusy
			}
			ar := s.article(id)
			ad, e := tx.Get(ar)
			if e != nil {
				return e
			}
			var a Article
			if e = ad.DataTo(&a); e != nil {
				return e
			}
			if !a.Active || a.Count <= 0 {
				j.Status = "cancelled"
				a.Status = "pending"
				a.Stage = ""
				if e = tx.Set(jr, j); e != nil {
					return e
				}
				return tx.Set(ar, a)
			}
			claimed = true
			j.Status = "running"
			j.Stage = "fetching"
			j.Attempts++
			j.Lease = token
			j.LeaseUntil = now.Add(5 * time.Minute)
			claimedJob = j
			a.Status = "processing"
			a.Stage = "fetching"
			a.Progress = "記事の本文を取得しています"
			a.Lease = token
			a.LeaseUntil = j.LeaseUntil
			a.LastError = ""
			if e = tx.Set(ar, a); e != nil {
				return e
			}
			return tx.Set(jr, j)
		})
		if err == nil {
			if !claimed {
				continue
			}
			claimedJob.ArticleID = id
			return &claimedJob, token, nil
		}
		if !errors.Is(err, ErrBusy) {
			return nil, "", err
		}
	}
	return nil, "", nil
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
		j.LeaseUntil = time.Now().Add(5 * time.Minute)
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
			if title != "" {
				a.Title = title
			}
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
