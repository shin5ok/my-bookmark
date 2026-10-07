package store

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestAPITokenPersistenceRotationAndRevocation(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-bookmark-test")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s := New(c)
	uid := Hash(RandomToken())
	raw := uid + "." + RandomToken()
	token := APIToken{User: User{ID: uid, Name: "reader"}, Email: "reader@data-cloud.jp", Hash: Hash(raw), ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Microsecond)}
	if err = s.PutAPIToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	defer s.DeleteAPIToken(ctx, uid)
	got, err := s.APIToken(ctx, uid)
	if err != nil || got.Hash != token.Hash || got.User != token.User || got.Email != token.Email || !got.ExpiresAt.Equal(token.ExpiresAt) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	other := APIToken{User: User{ID: Hash(RandomToken())}, Hash: Hash("other")}
	if err = s.PutAPIToken(ctx, other); err != nil {
		t.Fatal(err)
	}
	defer s.DeleteAPIToken(ctx, other.User.ID)
	token.Hash = Hash(uid + "." + RandomToken())
	if err = s.PutAPIToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	got, err = s.APIToken(ctx, uid)
	if err != nil || got.Hash != token.Hash {
		t.Fatal("rotation not persisted", err)
	}
	if err = s.DeleteAPIToken(ctx, uid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.APIToken(ctx, uid); !errors.Is(err, ErrNotFound) {
		t.Fatal("revoked token still exists", err)
	}
	if _, err = s.APIToken(ctx, other.User.ID); err != nil {
		t.Fatal("revocation affected other user", err)
	}
}
