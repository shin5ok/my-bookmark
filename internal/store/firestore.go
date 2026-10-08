package store

import (
	"context"
	"errors"
	"net/url"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Store struct{ client *firestore.Client }

func New(c *firestore.Client) *Store { return &Store{client: c} }
func missing(err error) bool         { return status.Code(err) == codes.NotFound }
func normalizeError(err error) error {
	if missing(err) {
		return ErrNotFound
	}
	return err
}
func (s *Store) article(id string) *firestore.DocumentRef {
	return s.client.Collection("articles").Doc(id)
}
func (s *Store) bookmark(uid, aid string) *firestore.DocumentRef {
	return s.client.Collection("bookmarks").Doc(Hash(uid + ":" + aid))
}
func (s *Store) Get(ctx context.Context, id string) (Article, error) {
	d, err := s.article(id).Get(ctx)
	if err != nil {
		return Article{}, normalizeError(err)
	}
	var a Article
	err = d.DataTo(&a)
	a.ID = d.Ref.ID
	return a, err
}
func (s *Store) Own(ctx context.Context, uid, aid string) (*Bookmark, error) {
	d, err := s.bookmark(uid, aid).Get(ctx)
	if err != nil {
		return nil, normalizeError(err)
	}
	var b Bookmark
	err = d.DataTo(&b)
	return &b, err
}

func (s *Store) SetUnderstood(ctx context.Context, uid, aid string, understood bool) error {
	_, err := s.bookmark(uid, aid).Update(ctx, []firestore.Update{
		{Path: "understood", Value: understood},
		{Path: "updated_at", Value: time.Now().UTC()},
	})
	return normalizeError(err)
}

func (s *Store) Save(ctx context.Context, u User, raw, comment string, tags []string) (Article, error) {
	id := Hash(raw)
	ar := s.article(id)
	br := s.bookmark(u.ID, id)
	jr := s.client.Collection("summary_jobs").Doc(id)
	parsed, err := url.Parse(raw)
	if err != nil {
		return Article{}, err
	}
	now := time.Now().UTC()
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		ad, ae := tx.Get(ar)
		if ae != nil && !missing(ae) {
			return ae
		}
		bd, be := tx.Get(br)
		if be != nil && !missing(be) {
			return be
		}
		jd, je := tx.Get(jr)
		if je != nil && !missing(je) {
			return je
		}
		a := Article{URL: raw, Title: parsed.Hostname(), Domain: parsed.Hostname(), CreatedAt: now, Status: "pending"}
		if ae == nil {
			if err := ad.DataTo(&a); err != nil {
				return err
			}
		}
		b := Bookmark{UserID: u.ID, ArticleID: id, Name: u.Name, CreatedAt: now}
		if be == nil {
			if err := bd.DataTo(&b); err != nil {
				return err
			}
		} else {
			a.Count++
		}
		b.Comment = comment
		b.Tags = tags
		b.UpdatedAt = now
		b.Name = u.Name
		a.Active = true
		ops := []func() error{}
		if ae != nil {
			q, qr, err := readQuota(ctx, tx, s.client, u.ID, now)
			if err != nil {
				return err
			}
			if consumeQuota(&q, now) {
				a.Status = "queued"
				a.Stage = "queued"
				a.Progress = "要約を開始します"
				j := SummaryJob{ArticleID: id, UserID: u.ID, Status: "queued", Stage: "queued", QueuedAt: now}
				ops = append(ops, func() error { return tx.Set(jr, j) })
			}
			ops = append(ops, func() error { return tx.Set(qr, q) })
		} else if je == nil && a.Status == "queued" {
			var j SummaryJob
			if err := jd.DataTo(&j); err != nil {
				return err
			}
			if j.Status == "queued" {
				a.Stage = "queued"
				a.Progress = "要約を開始します"
			}
		}
		if err := tx.Set(ar, a); err != nil {
			return err
		}
		if err := tx.Set(br, b); err != nil {
			return err
		}
		for _, op := range ops {
			if err := op(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Article{}, err
	}
	return s.Get(ctx, id)
}
func (s *Store) Delete(ctx context.Context, uid, aid string) error {
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		br := s.bookmark(uid, aid)
		bd, err := tx.Get(br)
		if err != nil {
			return normalizeError(err)
		}
		var b Bookmark
		if err = bd.DataTo(&b); err != nil {
			return err
		}
		ar := s.article(aid)
		d, err := tx.Get(ar)
		if err != nil {
			return err
		}
		var a Article
		if err = d.DataTo(&a); err != nil {
			return err
		}
		if a.Count > 0 {
			a.Count--
		}
		a.RatingTotal -= int64(b.Rating)
		a.Active = a.Count > 0
		jr := s.client.Collection("summary_jobs").Doc(aid)
		jd, je := tx.Get(jr)
		if je != nil && !missing(je) {
			return je
		}
		if !a.Active && je == nil {
			var j SummaryJob
			if err = jd.DataTo(&j); err != nil {
				return err
			}
			if j.Status == "queued" || j.Status == "dispatched" || j.Status == "running" {
				j.Status = "cancelled"
				j.Lease = ""
				j.LeaseUntil = time.Time{}
				a.Status = "pending"
				a.Stage = ""
				a.Progress = ""
				a.Lease = ""
				a.LeaseUntil = time.Time{}
				if err = tx.Set(jr, j); err != nil {
					return err
				}
			}
		}
		if err = tx.Set(ar, a); err != nil {
			return err
		}
		return tx.Delete(br)
	})
}

const PageSize = 20

func (s *Store) List(ctx context.Context, mode, uid, cursor string) ([]Entry, string, error) {
	mine := mode == "mine" || mode == "unread"
	var q firestore.Query
	if mine {
		q = s.client.Collection("bookmarks").Where("user_id", "==", uid).OrderBy("created_at", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc)
	} else {
		q = s.client.Collection("articles").Where("active", "==", true)
		if mode == "popular" {
			q = q.OrderBy("rating_total", firestore.Desc)
		} else {
			q = q.OrderBy("created_at", firestore.Desc)
		}
		q = q.OrderBy(firestore.DocumentID, firestore.Desc)
	}
	if cursor != "" {
		ref := s.article(cursor)
		if mine {
			ref = s.client.Collection("bookmarks").Doc(cursor)
		}
		d, err := ref.Get(ctx)
		if err != nil {
			return nil, "", normalizeError(err)
		}
		if mine {
			var b Bookmark
			if err = d.DataTo(&b); err != nil {
				return nil, "", err
			}
			if b.UserID != uid {
				return nil, "", ErrNotFound
			}
		}
		q = q.StartAfter(d)
	}
	// Filter after decoding so legacy bookmarks lacking understood are included.
	// Scan until we have a full matching page plus one for the next-page link.
	var docs []*firestore.DocumentSnapshot
	for len(docs) <= PageSize {
		batch, err := q.Limit(PageSize + 1).Documents(ctx).GetAll()
		if err != nil {
			return nil, "", err
		}
		for _, d := range batch {
			if mode == "unread" {
				var bookmark Bookmark
				if err := d.DataTo(&bookmark); err != nil {
					return nil, "", err
				}
				if bookmark.Understood {
					continue
				}
			}
			docs = append(docs, d)
			if len(docs) > PageSize {
				break
			}
		}
		if len(batch) < PageSize+1 || len(docs) > PageSize {
			break
		}
		q = q.StartAfter(batch[len(batch)-1])
	}
	var err error
	next := ""
	if len(docs) > PageSize {
		next = docs[PageSize-1].Ref.ID
		docs = docs[:PageSize]
	}
	entries := make([]Entry, 0, len(docs))
	for _, d := range docs {
		var e Entry
		if mine {
			var b Bookmark
			if err = d.DataTo(&b); err != nil {
				return nil, "", err
			}
			e.Bookmark = &b
			e.Article, err = s.Get(ctx, b.ArticleID)
		} else {
			err = d.DataTo(&e.Article)
			e.Article.ID = d.Ref.ID
			if err == nil && uid != "" {
				e.Bookmark, err = s.Own(ctx, uid, e.Article.ID)
				if errors.Is(err, ErrNotFound) {
					err = nil
				}
			}
		}
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, e)
	}
	return entries, next, nil
}
func (s *Store) Comments(ctx context.Context, aid string) ([]Bookmark, error) {
	docs, err := s.client.Collection("bookmarks").Where("article_id", "==", aid).OrderBy("created_at", firestore.Desc).Limit(50).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	result := make([]Bookmark, 0, len(docs))
	for _, d := range docs {
		var b Bookmark
		if err = d.DataTo(&b); err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, nil
}
