package store

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"my-bookmark/internal/summary"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTLDRChangesOnlyWithSuccessfulOwnedJob(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-tldr-"+Hash(RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	u := User{ID: Hash(RandomToken()), Name: "reader"}
	a, err := s.Save(ctx, u, "https://example.com/"+RandomToken(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, lease, err := s.NextQueued(ctx)
	if err != nil || lease == "" {
		t.Fatal("claim", err)
	}
	original := []string{"結論", "重要性", "影響"}
	if err = s.FinishJob(ctx, a.ID, "wrong", "不正タイトル", []string{"不正本文"}, "test", nil, "", []string{"不正", "更新"}); !errors.Is(err, ErrBusy) {
		t.Fatal("stale worker accepted", err)
	}
	if err = s.FinishJob(ctx, a.ID, lease, "初回タイトル", []string{"初回本文"}, "test", nil, "", original); err != nil {
		t.Fatal(err)
	}
	check := func(title, point string) {
		t.Helper()
		got, e := s.Get(ctx, a.ID)
		if e != nil || got.Title != title || len(got.TLDR) != 3 || got.TLDR[0] != original[0] || got.Points[0] != point {
			t.Fatalf("inconsistent result: %+v %v", got, e)
		}
	}
	check("初回タイトル", "初回本文")
	if _, err = s.Enqueue(ctx, "other-reader", a.ID, summary.Standard, "英語で7項目"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unowned request accepted: %v", err)
	}
	if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Standard, strings.Repeat("詳", 201)); err == nil {
		t.Fatal("overlong instruction accepted")
	}
	if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Standard, "英語で7項目"); err != nil {
		t.Fatal(err)
	}
	check("初回タイトル", "初回本文")
	job, lease, err := s.NextQueued(ctx)
	if err != nil || job == nil || job.Instruction != "英語で7項目" {
		t.Fatalf("instruction missing: %+v %v", job, err)
	}
	if err = s.RetryJob(ctx, a.ID, lease); err != nil {
		t.Fatal(err)
	}
	job, lease, err = s.ClaimJob(ctx, a.ID, job.QueuedAt)
	if err != nil || job == nil || job.Instruction != "英語で7項目" {
		t.Fatalf("retry lost instruction: %+v %v", job, err)
	}
	if err = s.FinishJob(ctx, a.ID, lease, "失敗タイトル", []string{"失敗本文"}, "test", nil, "generation failed", []string{"不完全", "データ"}); err != nil {
		t.Fatal(err)
	}
	check("初回タイトル", "初回本文")
	if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Simple, ""); err != nil {
		t.Fatal(err)
	}
	job, lease, err = s.NextQueued(ctx)
	if err != nil || job == nil || job.Instruction != "" {
		t.Fatalf("previous instruction reused: %+v %v", job, err)
	}
	original = []string{"新しい結論", "新しい重要性", "新しい影響"}
	if err = s.FinishJob(ctx, a.ID, lease, "更新タイトル", []string{"更新本文"}, "test", nil, "", original); err != nil {
		t.Fatal(err)
	}
	check("更新タイトル", "更新本文")
}

func TestFailedSummarySavesTitle(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-title-"+Hash(RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	u := User{ID: Hash(RandomToken()), Name: "reader"}
	a, err := s.Save(ctx, u, "https://example.com/"+RandomToken(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"短い日本語タイトル", ""} {
		_, lease, err := s.NextQueued(ctx)
		if err != nil || lease == "" {
			t.Fatal("claim", err)
		}
		if err = s.FinishJob(ctx, a.ID, lease, title, nil, "test", nil, "記事の情報が不足していて要約を作成できませんでした。", nil); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, a.ID)
		if err != nil || got.Title != "短い日本語タイトル" || got.Status != "failed" || len(got.Points) != 0 || len(got.TLDR) != 0 {
			t.Fatalf("failed summary must save a valid title and preserve it on empty retry: %+v %v", got, err)
		}
		if title != "" {
			if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Concrete, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
}
