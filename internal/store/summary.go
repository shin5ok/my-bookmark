package store

import (
	"context"
	"time"

	"cloud.google.com/go/firestore"
)

type quota struct {
	Minute      time.Time `firestore:"minute"`
	MinuteCount int       `firestore:"minute_count"`
	Day         string    `firestore:"day"`
	DayCount    int       `firestore:"day_count"`
	ExpiresAt   time.Time `firestore:"expires_at"`
}

func (s *Store) Claim(ctx context.Context, uid, aid string) (string, error) {
	lease := RandomToken()
	now := time.Now().UTC()
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := tx.Get(s.bookmark(uid, aid)); err != nil {
			return normalizeError(err)
		}
		ar := s.article(aid)
		d, err := tx.Get(ar)
		if err != nil {
			return normalizeError(err)
		}
		var a Article
		if err = d.DataTo(&a); err != nil {
			return err
		}
		if a.Status == "ready" || a.LeaseUntil.After(now) {
			return ErrBusy
		}
		qr := s.client.Collection("quotas").Doc(uid)
		qd, qe := tx.Get(qr)
		if qe != nil && !missing(qe) {
			return qe
		}
		var q quota
		if qe == nil {
			if err = qd.DataTo(&q); err != nil {
				return err
			}
		}
		if !q.Minute.Equal(now.Truncate(time.Minute)) {
			q.Minute = now.Truncate(time.Minute)
			q.MinuteCount = 0
		}
		if q.Day != now.Format("2006-01-02") {
			q.Day = now.Format("2006-01-02")
			q.DayCount = 0
		}
		if q.MinuteCount >= 3 || q.DayCount >= 50 {
			return ErrRateLimited
		}
		q.MinuteCount++
		q.DayCount++
		q.ExpiresAt = now.Add(48 * time.Hour)
		a.Lease = lease
		a.LeaseUntil = now.Add(2 * time.Minute)
		a.Status = "processing"
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		return tx.Set(qr, q)
	})
	return lease, err
}
func (s *Store) Finish(ctx context.Context, aid, lease, title string, points []string, model string, success bool) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ar := s.article(aid)
		d, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var a Article
		if err = d.DataTo(&a); err != nil {
			return err
		}
		if a.Lease != lease {
			return ErrBusy
		}
		a.Lease = ""
		a.LeaseUntil = time.Time{}
		a.Status = "failed"
		if success {
			a.Status = "ready"
			a.Points = points
			a.Model = model
			if title != "" {
				a.Title = title
			}
		}
		return tx.Set(ar, a)
	})
}
