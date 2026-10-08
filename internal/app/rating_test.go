package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"my-bookmark/internal/store"
)

func TestRatingHTTPAndUnreadFlow(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-rating-ui-"+store.Hash(store.RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	db := store.New(c)
	u := store.User{ID: "reader", Name: "reader"}
	for _, uid := range []string{"reader", "other"} {
		if err = db.PutSession(ctx, uid, store.Session{User: store.User{ID: uid}, CSRF: "correct", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	unread, err := db.Save(ctx, u, "https://example.com/unread", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	understood, err := db.Save(ctx, u, "https://example.com/understood", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SetUnderstood(ctx, u.ID, understood.ID, true); err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Env: "development", BaseURL: "http://localhost:8080"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		uid, csrf, rating string
		status            int
	}{
		{"", "correct", "5", 401}, {"reader", "wrong", "5", 403}, {"reader", "correct", "6", 400}, {"reader", "correct", "-1", 400}, {"reader", "correct", "bad", 400}, {"other", "correct", "5", 404}, {"reader", "correct", "5", 200}, {"reader", "correct", "5", 200},
	} {
		body := url.Values{"csrf": {tc.csrf}, "rating": {tc.rating}}
		r := httptest.NewRequest("POST", "/bookmarks/"+unread.ID+"/rating", strings.NewReader(body.Encode())).WithContext(ctx)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if tc.uid != "" {
			r.AddCookie(&http.Cookie{Name: "shiori_session", Value: tc.uid})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%+v: HTTP %d %s", tc, w.Code, w.Body.String())
		}
	}
	got, err := db.Get(ctx, unread.ID)
	if err != nil || got.RatingTotal != 5 {
		t.Fatalf("rating total: %+v %v", got, err)
	}
	r := httptest.NewRequest("GET", "/mine?filter=unread", nil).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "reader"})
	w := httptest.NewRecorder()
	a.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-article="`+unread.ID+`"`) || strings.Contains(w.Body.String(), `data-article="`+understood.ID+`"`) {
		t.Fatalf("incorrect unread page: %d %s", w.Code, w.Body.String())
	}
	if _, err = db.SetRating(ctx, u.ID, unread.ID, 0); err != nil {
		t.Fatal(err)
	}
	t.Run("browser", func(t *testing.T) { checkRatingInBrowser(t, a, db, unread.ID) })
}

func checkRatingInBrowser(t *testing.T, a *App, db *store.Store, id string) {
	t.Helper()
	chrome := ""
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			chrome = path
			break
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium required")
	}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	a.cfg.BaseURL = server.URL
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	if os.Getuid() == 0 {
		options = append(options, chromedp.NoSandbox)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), options...)
	defer stop()
	browser, stop := chromedp.NewContext(alloc)
	defer stop()
	ctx, cancel := context.WithTimeout(browser, 30*time.Second)
	defer cancel()
	for _, width := range []int64{390, 1280} {
		if err := db.SetUnderstood(ctx, "reader", id, false); err != nil {
			t.Fatal(err)
		}
		var filled int
		var overflow bool
		var location string
		if err := chromedp.Run(ctx,
			chromedp.EmulateViewport(width, 900), network.SetCookie("shiori_session", "reader").WithURL(server.URL),
			chromedp.Navigate(server.URL+"/mine"), chromedp.Click(".mine-submenu", chromedp.ByQuery),
			chromedp.WaitVisible(".feed[data-filter=unread] .rating-star", chromedp.ByQuery),
			chromedp.Click(".rating-star[data-rating='3']", chromedp.ByQuery),
			chromedp.WaitVisible(".rating-controls[data-rating='3']:not([data-busy])", chromedp.ByQuery),
			chromedp.Reload(), chromedp.WaitVisible(".rating-controls[data-rating='3']", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelectorAll('.rating-star[data-filled=true]').length`, &filled),
			chromedp.Evaluate(`document.documentElement.scrollWidth > innerWidth`, &overflow),
		); err != nil {
			t.Fatal(err)
		}
		if filled != 3 || overflow {
			t.Fatalf("width=%d filled=%d overflow=%v", width, filled, overflow)
		}
		if err := chromedp.Run(ctx,
			chromedp.Click(".rating-star[data-rating='5']", chromedp.ByQuery),
			chromedp.WaitVisible(".rating-controls[data-rating='5']:not([data-busy])", chromedp.ByQuery),
			chromedp.Click(".rating-clear", chromedp.ByQuery),
			chromedp.WaitVisible(".rating-controls[data-rating='0']:not([data-busy])", chromedp.ByQuery),
			chromedp.Click(".understood-button", chromedp.ByQuery),
			chromedp.WaitVisible(".empty", chromedp.ByQuery), chromedp.Location(&location),
		); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(location, "filter=unread") {
			t.Fatalf("lost filter: %s", location)
		}
		b, err := db.Own(ctx, "reader", id)
		if err != nil || b.Rating != 0 || !b.Understood {
			t.Fatalf("UI state not persisted: %+v %v", b, err)
		}
	}
}
