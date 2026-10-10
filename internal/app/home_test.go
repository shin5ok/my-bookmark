package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"my-bookmark/internal/store"
)

type homeTestDB struct {
	testDB
	mode, uid string
}

func (d *homeTestDB) List(_ context.Context, mode, uid, _ string) ([]store.Entry, string, error) {
	d.mode, d.uid = mode, uid
	return nil, strings.Repeat("a", 64), nil
}

func TestHomeDisplaysMyBookmarks(t *testing.T) {
	for _, tc := range []struct{ path, mode, next string }{
		{"/", "mine", "/?cursor="},
		{"/?filter=unread", "unread", "/?cursor="},
		{"/mine", "mine", "/?cursor="},
		{"/mine?filter=unread", "unread", "/?cursor="},
		{"/new", "new", "/new?cursor="},
		{"/popular", "popular", "/popular?cursor="},
	} {
		t.Run(tc.path, func(t *testing.T) {
			a := testApp(t)
			db := &homeTestDB{}
			a.db = db
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "token"})
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != http.StatusOK || db.mode != tc.mode || db.uid != "user" {
				t.Fatalf("status=%d mode=%s owner=%s", w.Code, db.mode, db.uid)
			}
			next := tc.next + strings.Repeat("a", 64)
			if tc.mode == "unread" {
				next += "&amp;filter=unread"
			}
			if !strings.Contains(w.Body.String(), `href="`+next+`"`) {
				t.Fatalf("pagination lost mode: %s", w.Body.String())
			}
			if tc.mode == "mine" && !strings.Contains(w.Body.String(), `href="/" aria-current="page"`) {
				t.Fatal("home menu not active")
			}
		})
	}
}

func TestHomeRequiresLogin(t *testing.T) {
	a := testApp(t)
	for _, path := range []string{"/", "/?filter=unread", "/mine"} {
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/auth/google" {
			t.Fatalf("%s: status=%d location=%s", path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestPublicFeedsUseSeparatePaths(t *testing.T) {
	for _, tc := range []struct{ path, mode string }{
		{"/new", "new"}, {"/popular", "popular"}, {"/new?sort=popular", "new"}, {"/popular?sort=new", "popular"},
	} {
		a := testApp(t)
		db := &homeTestDB{}
		a.db = db
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusOK || db.mode != tc.mode || db.uid != "" {
			t.Fatalf("%s: status=%d mode=%s uid=%s", tc.path, w.Code, db.mode, db.uid)
		}
		if !strings.Contains(w.Body.String(), `href="/`+tc.mode+`" aria-current="page"`) {
			t.Fatal("public feed menu not active")
		}
	}
}

func TestLegacyFeedLinksRedirectToSeparatePaths(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/?sort=new", "/new"},
		{"/?sort=popular", "/popular"},
		{"/?sort=popular&cursor=" + strings.Repeat("a", 64), "/popular?cursor=" + strings.Repeat("a", 64)},
	} {
		a := testApp(t)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != tc.want {
			t.Fatalf("%s: status=%d location=%s", tc.path, w.Code, w.Header().Get("Location"))
		}
	}
}
