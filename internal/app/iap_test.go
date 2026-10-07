package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"my-bookmark/internal/iap"
	"my-bookmark/internal/store"
)

type iapTestVerifier struct{ identity iap.Identity }

func (v iapTestVerifier) Verify(context.Context, string) (iap.Identity, error) {
	return v.identity, nil
}

func TestLoadConfigRequiresValidAllowAccountsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "allow_accounts.yaml")
	t.Setenv("APP_ENV", "production")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("BASE_URL", "https://bookmark.example")
	t.Setenv("IAP_AUDIENCE", "/projects/123/locations/asia-northeast1/services/shiori")
	t.Setenv("ALLOW_ACCOUNTS_FILE", path)
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing allowlist accepted")
	}
	if err := os.WriteFile(path, []byte("domains: [data-cloud.jp]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil || !cfg.IAPAllowlist.Allows("reader@data-cloud.jp") {
		t.Fatalf("allowlist not loaded: %v", err)
	}
}

type iapTestDB struct {
	testDB
	created *store.Session
}

func (d *iapTestDB) Session(context.Context, string) (store.Session, error) {
	return store.Session{}, store.ErrNotFound
}
func (d *iapTestDB) PutSession(_ context.Context, _ string, s store.Session) error {
	d.created = &s
	return nil
}

func TestIAPAllowlistGuardsSessionCreation(t *testing.T) {
	list, err := iap.ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		email string
		want  int
	}{
		{"reader@data-cloud.jp", http.StatusOK},
		{"reader@other.jp", http.StatusForbidden},
	} {
		t.Run(tc.email, func(t *testing.T) {
			db := &iapTestDB{}
			a, err := New(Config{Env: "production", BaseURL: "https://bookmark.example", IAPAudience: "/projects/123/locations/asia-northeast1/services/shiori", IAPAllowlist: list}, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.iapVerifier = iapTestVerifier{iap.Identity{Email: tc.email, Subject: "subject"}}
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("X-Goog-IAP-JWT-Assertion", "signed-token")
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if (db.created != nil) != (tc.want == http.StatusOK) {
				t.Fatalf("session created=%t", db.created != nil)
			}
			if tc.want == http.StatusOK && db.created.User.Name != "reader" {
				t.Fatalf("public display name=%q", db.created.User.Name)
			}
		})
	}
}

func TestIAPModeRejectsCookieWithoutSignedAssertion(t *testing.T) {
	list, err := iap.ParseAllowlist([]byte("domains: [data-cloud.jp]"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Env: "production", BaseURL: "https://bookmark.example", IAPAudience: "/projects/123/locations/asia-northeast1/services/shiori", IAPAllowlist: list}, testDB{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-shiori_session", Value: "existing-session"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", w.Code)
	}
}
