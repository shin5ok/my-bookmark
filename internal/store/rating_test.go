package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func ratingTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	c, err := firestore.NewClient(ctx, "demo-ratings-"+Hash(RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return New(c), ctx
}

func TestRatingsUpdateAndDeleteWithoutDoubleCounting(t *testing.T) {
	s, ctx := ratingTestStore(t)
	u, v := User{ID: "u"}, User{ID: "v"}
	a, err := s.Save(ctx, u, "https://example.com/article", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, v, a.URL, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		uid    string
		rating int
		want   int64
	}{{"u", 5, 5}, {"v", 4, 9}, {"u", 2, 6}, {"u", 2, 6}, {"v", 0, 2}} {
		total, err := s.SetRating(ctx, tc.uid, a.ID, tc.rating)
		if err != nil || total != tc.want {
			t.Fatalf("%+v: total=%d err=%v", tc, total, err)
		}
	}
	if _, err = s.SetRating(ctx, "other", a.ID, 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner rated: %v", err)
	}
	for _, value := range []int{-1, 6} {
		if _, err = s.SetRating(ctx, u.ID, a.ID, value); !errors.Is(err, ErrInvalidRating) {
			t.Fatal(err)
		}
	}
	if _, err = s.Save(ctx, u, a.URL, "edited", nil); err != nil {
		t.Fatal(err)
	}
	b, err := s.Own(ctx, u.ID, a.ID)
	if err != nil || b.Rating != 2 {
		t.Fatalf("edit erased rating: %+v %v", b, err)
	}
	var wg sync.WaitGroup
	for _, uid := range []string{u.ID, v.ID} {
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			if _, e := s.SetRating(ctx, uid, a.ID, 5); e != nil {
				t.Error(e)
			}
		}(uid)
	}
	wg.Wait()
	got, err := s.Get(ctx, a.ID)
	if err != nil || got.RatingTotal != 10 {
		t.Fatalf("concurrent rating total: %+v %v", got, err)
	}
	if err = s.Delete(ctx, u.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, a.ID)
	if err != nil || got.RatingTotal != 5 || got.Count != 1 {
		t.Fatalf("delete total: %+v %v", got, err)
	}
	if err = s.Delete(ctx, v.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, a.ID)
	if err != nil || got.RatingTotal != 0 || got.Active {
		t.Fatalf("last delete: %+v %v", got, err)
	}
}

func TestUnreadPaginationIncludesLegacyBookmarks(t *testing.T) {
	s, ctx := ratingTestStore(t)
	batch := s.client.Batch()
	now := time.Now()
	for i := 0; i < 66; i++ {
		id := Hash(fmt.Sprintf("article-%d", i))
		batch.Set(s.article(id), Article{Title: fmt.Sprint(i), Active: true, CreatedAt: now.Add(time.Duration(i) * time.Second)})
		data := map[string]any{"user_id": "reader", "article_id": id, "created_at": now.Add(time.Duration(i) * time.Second)}
		// Newest 45 are understood. Older 21 lack the legacy field entirely.
		if i >= 21 {
			data["understood"] = true
		}
		batch.Set(s.bookmark("reader", id), data)
	}
	if _, err := batch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	page, next, err := s.List(ctx, "unread", "reader", "")
	if err != nil || len(page) != 20 || next == "" {
		t.Fatalf("first page=%d next=%s err=%v", len(page), next, err)
	}
	for i, e := range page {
		if e.Bookmark.Understood || e.Article.Title != fmt.Sprint(20-i) {
			t.Fatalf("wrong unread order: %+v", e)
		}
	}
	last, end, err := s.List(ctx, "unread", "reader", next)
	if err != nil || len(last) != 1 || end != "" || last[0].Article.Title != "0" {
		t.Fatalf("last page=%+v next=%s err=%v", last, end, err)
	}
	if _, _, err = s.List(ctx, "unread", "other", next); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's cursor accepted: %v", err)
	}
	if err = s.SetUnderstood(ctx, "reader", page[0].Article.ID, true); err != nil {
		t.Fatal(err)
	}
	page, next, err = s.List(ctx, "unread", "reader", "")
	if err != nil || len(page) != 20 || next != "" || page[0].Article.Title != "19" {
		t.Fatalf("understood still in unread: %+v %s %v", page, next, err)
	}
}

func TestPopularUsesStarsAndMigrationPreservesRatings(t *testing.T) {
	s, ctx := ratingTestStore(t)
	batch := s.client.Batch()
	for i := 0; i < 45; i++ {
		id := Hash(fmt.Sprint(i))
		batch.Set(s.article(id), Article{Active: true, RatingTotal: int64(i % 7), Count: int64(100 - i), CreatedAt: time.Now()})
	}
	legacy := s.article(Hash("legacy"))
	batch.Set(legacy, map[string]any{"active": true, "count": 999, "created_at": time.Now()})
	if _, err := batch.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := s.InitializeRatingTotals(ctx); err != nil || n != 1 {
		t.Fatalf("migration: %d %v", n, err)
	}
	if n, err := s.InitializeRatingTotals(ctx); err != nil || n != 0 {
		t.Fatalf("repeat migration: %d %v", n, err)
	}
	cursor := ""
	seen := map[string]bool{}
	previous := int64(99)
	for {
		entries, next, err := s.List(ctx, "popular", "", cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if seen[e.Article.ID] || e.Article.RatingTotal > previous {
				t.Fatalf("incorrect pagination/order: %+v", e.Article)
			}
			seen[e.Article.ID] = true
			previous = e.Article.RatingTotal
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 46 || !seen[legacy.ID] {
		t.Fatalf("missing articles: %d", len(seen))
	}
	got, err := s.Get(ctx, Hash("6"))
	if err != nil || got.RatingTotal != 6 {
		t.Fatalf("migration overwrote rating: %+v %v", got, err)
	}
}
