package store

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"my-bookmark/internal/summary"
)

func TestTaskClaimsSurviveRedeliveryAndRejectOldGenerations(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-tasks-"+Hash(RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	u := User{ID: Hash(RandomToken()), Name: "reader"}
	a, err := s.Save(ctx, u, "https://youtu.be/3KtWfp0UopM", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.QueuedJobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("outbox %v %v", jobs, err)
	}
	first := jobs[0]
	if err = s.MarkDispatched(ctx, a.ID, first.QueuedAt); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Simple, ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("dispatched job must remain busy: %v", err)
	}
	jobs, err = s.QueuedJobs(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("dispatched job remains in outbox: %v %v", jobs, err)
	}
	// Two concurrent Cloud Tasks deliveries must yield only one lease.
	var wg sync.WaitGroup
	leases := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, lease, e := s.ClaimJob(ctx, a.ID, first.QueuedAt)
			if e == nil && job != nil {
				leases <- lease
			} else if !errors.Is(e, ErrBusy) {
				t.Errorf("unexpected claim: %v %v", job, e)
			}
		}()
	}
	wg.Wait()
	close(leases)
	var lease string
	count := 0
	for value := range leases {
		lease = value
		count++
	}
	if count != 1 {
		t.Fatalf("got %d owners", count)
	}
	if err = s.MarkDispatched(ctx, a.ID, first.QueuedAt); err != nil {
		t.Fatal(err)
	}
	if err = s.RetryJob(ctx, a.ID, "stale"); !errors.Is(err, ErrBusy) {
		t.Fatalf("stale retry: %v", err)
	}
	if err = s.RetryJob(ctx, a.ID, lease); err != nil {
		t.Fatal(err)
	}
	job, lease, err := s.ClaimJob(ctx, a.ID, first.QueuedAt)
	if err != nil || job == nil || job.Attempts != 2 {
		t.Fatalf("retry: %+v %v", job, err)
	}
	oldLease := lease
	_, err = c.Collection("summary_jobs").Doc(a.ID).Update(ctx, []firestore.Update{{Path: "lease_until", Value: time.Now().Add(-time.Minute)}})
	if err != nil {
		t.Fatal(err)
	}
	job, lease, err = s.ClaimJob(ctx, a.ID, first.QueuedAt)
	if err != nil || job == nil || lease == oldLease || job.Attempts != 3 {
		t.Fatalf("expired lease: %+v %v", job, err)
	}
	if err = s.FinishJob(ctx, a.ID, oldLease, "old", nil, "test", nil, "", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("old worker overwrite: %v", err)
	}
	if err = s.FinishJob(ctx, a.ID, lease, "動画タイトル", []string{"要点"}, "test", nil, "", []string{"結論", "理由"}); err != nil {
		t.Fatal(err)
	}
	if job, _, err = s.ClaimJob(ctx, a.ID, first.QueuedAt); err != nil || job != nil {
		t.Fatalf("completed task reclaimed: %+v %v", job, err)
	}
	if _, err = s.Enqueue(ctx, u.ID, a.ID, summary.Simple, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDispatched(ctx, a.ID, first.QueuedAt); err != nil {
		t.Fatal(err)
	}
	if job, _, err = s.ClaimJob(ctx, a.ID, first.QueuedAt); err != nil || job != nil {
		t.Fatalf("old task claimed new request: %+v %v", job, err)
	}
	jobs, err = s.QueuedJobs(ctx)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("new request disappeared: %v %v", jobs, err)
	}
	job, lease, err = s.ClaimJob(ctx, a.ID, jobs[0].QueuedAt)
	if err != nil || job == nil || job.Attempts != 1 {
		t.Fatalf("new generation: %+v %v", job, err)
	}
	if err = s.Delete(ctx, u.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.FinishJob(ctx, a.ID, lease, "cancelled", nil, "test", nil, "", nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("cancelled task overwrote result: %v", err)
	}
}
