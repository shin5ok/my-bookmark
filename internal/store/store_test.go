package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestBookmarkLifecycle(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator; run make test-integration")
	}
	ctx := context.Background()
	client, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	s := New(client)
	u := User{ID: RandomToken(), Name: "test"}
	v := User{ID: RandomToken(), Name: "other"}
	raw := "https://example.com/" + RandomToken()
	a, err := s.Save(ctx, u, raw, "first", []string{"go"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Delete(ctx, u.ID, a.ID)
	if _, err = s.Save(ctx, u, raw, "updated", nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, a.ID)
	if err != nil || got.Count != 1 {
		t.Fatalf("duplicate save: %#v %v", got, err)
	}
	if err = s.Delete(ctx, v.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's delete: %v", err)
	}
	lease, err := s.Claim(ctx, u.ID, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, u.ID, a.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate generation: %v", err)
	}
	if err = s.Finish(ctx, a.ID, "wrong", "title", []string{"wrong"}, "model", true); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale worker: %v", err)
	}
	if err = s.Finish(ctx, a.ID, lease, "title", []string{"point"}, "model", true); err != nil {
		t.Fatal(err)
	}
	entries, _, err := s.List(ctx, "mine", u.ID, "")
	if err != nil || len(entries) != 1 || entries[0].Bookmark.Comment != "updated" {
		t.Fatalf("list: %#v %v", entries, err)
	}
	if err = s.Delete(ctx, u.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, a.ID)
	if err != nil || got.Count != 0 || got.Active {
		t.Fatalf("delete count: %#v %v", got, err)
	}
}
func TestExpiredSession(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx := context.Background()
	c, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	token := RandomToken()
	session := Session{User: User{ID: "u"}, ExpiresAt: time.Now().Add(-time.Hour)}
	if err = s.PutSession(ctx, token, session); err != nil {
		t.Fatal(err)
	}
	defer s.DeleteSession(ctx, token)
	if _, err = s.Session(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session accepted: %v", err)
	}
}

func TestConcurrentBookmarksAndQuota(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	u := User{ID: RandomToken(), Name: "reader"}
	raw := "https://example.com/" + RandomToken()
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { _, e := s.Save(ctx, u, raw, "", nil); results <- e }()
	}
	for i := 0; i < 8; i++ {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	article, err := s.Get(ctx, Hash(raw))
	if err != nil || article.Count != 1 {
		t.Fatalf("concurrent saves: %#v %v", article, err)
	}
	defer s.Delete(ctx, u.ID, article.ID)
	if _, err = s.Claim(ctx, "other-user", article.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner summary: %v", err)
	}
	// Freeze the quota window in storage for a deterministic limit assertion.
	now := time.Now().UTC()
	_, err = c.Collection("quotas").Doc(u.ID).Set(ctx, quota{Minute: now.Truncate(time.Minute), MinuteCount: 3, Day: now.Format("2006-01-02"), DayCount: 50})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, u.ID, article.ID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("quota not enforced: %v", err)
	}
}
func TestOAuthStateSingleUse(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	key := RandomToken()
	if err = s.PutOAuth(ctx, key, OAuthState{Nonce: "nonce", Verifier: "verifier", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if state, err := s.ConsumeOAuth(ctx, key); err != nil || state.Nonce != "nonce" {
		t.Fatalf("state: %#v %v", state, err)
	}
	if _, err = s.ConsumeOAuth(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("state replay: %v", err)
	}
}
