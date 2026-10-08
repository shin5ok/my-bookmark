package store

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/firestore"
)

var ErrInvalidRating = errors.New("rating must be between 0 and 5")

// SetRating changes a user's rating and the shared total atomically. Zero clears it.
func (s *Store) SetRating(ctx context.Context, uid, aid string, rating int) (int64, error) {
	if rating < 0 || rating > 5 {
		return 0, ErrInvalidRating
	}
	var total int64
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		br, ar := s.bookmark(uid, aid), s.article(aid)
		bd, err := tx.Get(br)
		if err != nil {
			return normalizeError(err)
		}
		ad, err := tx.Get(ar)
		if err != nil {
			return normalizeError(err)
		}
		var b Bookmark
		var a Article
		if err = bd.DataTo(&b); err != nil {
			return err
		}
		if err = ad.DataTo(&a); err != nil {
			return err
		}
		total = a.RatingTotal + int64(rating-b.Rating)
		if err = tx.Update(br, []firestore.Update{{Path: "rating", Value: rating}, {Path: "updated_at", Value: time.Now().UTC()}}); err != nil {
			return err
		}
		return tx.Update(ar, []firestore.Update{{Path: "rating_total", Value: total}})
	})
	return total, err
}

// InitializeRatingTotals backfills the field required by Firestore's orderBy.
// Each transaction rechecks the document to preserve concurrent ratings.
func (s *Store) InitializeRatingTotals(ctx context.Context) (int, error) {
	updated := 0
	q := s.client.Collection("articles").OrderBy(firestore.DocumentID, firestore.Asc).Limit(200)
	for {
		docs, err := q.Documents(ctx).GetAll()
		if err != nil {
			return updated, err
		}
		if len(docs) == 0 {
			return updated, nil
		}
		for _, doc := range docs {
			if _, exists := doc.Data()["rating_total"]; exists {
				continue
			}
			changed := false
			err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
				changed = false
				fresh, err := tx.Get(doc.Ref)
				if missing(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if _, exists := fresh.Data()["rating_total"]; exists {
					return nil
				}
				changed = true
				return tx.Update(doc.Ref, []firestore.Update{{Path: "rating_total", Value: int64(0)}})
			})
			if err != nil {
				return updated, err
			}
			if changed {
				updated++
			}
		}
		q = q.StartAfter(docs[len(docs)-1])
	}
}
