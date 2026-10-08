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

func TestUnderstoodHTTPFlow(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := firestore.NewClient(ctx, "demo-understood-"+store.Hash(store.RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	db := store.New(c)
	user := store.User{ID: "reader", Name: "reader"}
	if err = db.PutSession(ctx, "token", store.Session{User: user, CSRF: "correct", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	article, err := db.Save(ctx, user, "https://example.com/article", "comment", []string{"技術"})
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{BaseURL: "http://localhost:8080", Env: "development"}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path, token string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(values.Encode())).WithContext(ctx)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if token != "" {
			r.AddCookie(&http.Cookie{Name: "shiori_session", Value: token})
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, r)
		return w
	}
	path := "/bookmarks/" + article.ID + "/understood"
	for _, tc := range []struct {
		token, csrf, value string
		status             int
	}{
		{"", "correct", "true", 401},
		{"token", "wrong", "true", 403},
		{"token", "correct", "invalid", 400},
		{"token", "correct", "true", 200},
		{"token", "correct", "true", 200},
	} {
		w := request("POST", path, tc.token, url.Values{"csrf": {tc.csrf}, "understood": {tc.value}})
		if w.Code != tc.status {
			t.Fatalf("token=%q csrf=%q value=%q: status=%d want=%d body=%s", tc.token, tc.csrf, tc.value, w.Code, tc.status, w.Body.String())
		}
	}
	check := func(understood bool) {
		t.Helper()
		for _, page := range []string{"/mine", "/articles/" + article.ID} {
			w := request("GET", page, "token", nil)
			body := w.Body.String()
			if w.Code != 200 || strings.Contains(body, `class="bookmark is-understood is-collapsed"`) != understood || strings.Contains(body, `aria-pressed="true"`) != understood || !strings.Contains(body, "understood-button") {
				t.Fatalf("state missing on %s: %d %s", page, w.Code, body)
			}
		}
	}
	check(true)
	if _, err = db.Save(ctx, user, article.URL, "edited", nil); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err = db.PutSession(ctx, "other", store.Session{User: store.User{ID: "other"}, CSRF: "correct", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", path, "other", url.Values{"csrf": {"correct"}, "understood": {"false"}}); w.Code != 404 {
		t.Fatalf("non-owner update: %d", w.Code)
	}
	check(true)
	if w := request("GET", "/articles/"+article.ID, "other", nil); strings.Contains(w.Body.String(), "understood-button") || strings.Contains(w.Body.String(), "is-understood") {
		t.Fatal("understanding state leaked to another user")
	}
	if w := request("POST", path, "token", url.Values{"csrf": {"correct"}, "understood": {"false"}}); w.Code != 200 {
		t.Fatalf("undo: %d %s", w.Code, w.Body.String())
	}
	check(false)
	got, err := db.Get(ctx, article.ID)
	if err != nil || got.Count != 1 {
		t.Fatalf("understanding changed bookmark count: %+v %v", got, err)
	}
	t.Run("browser", func(t *testing.T) { checkUnderstoodInBrowser(t, a) })
}

func checkUnderstoodInBrowser(t *testing.T, a *App) {
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
	ctx, cancel := context.WithTimeout(browser, 15*time.Second)
	defer cancel()
	for _, width := range []int64{390, 1280} {
		var before, after, location, label string
		var overflow, titleOnly, expanded bool
		if err := chromedp.Run(ctx,
			chromedp.EmulateViewport(width, 900),
			network.SetCookie("shiori_session", "token").WithURL(server.URL),
			chromedp.Navigate(server.URL+"/mine"),
			chromedp.WaitVisible(".understood-button", chromedp.ByQuery),
			chromedp.Evaluate(`getComputedStyle(document.querySelector('.bookmark')).backgroundColor`, &before),
			chromedp.Click(".understood-button", chromedp.ByQuery),
			chromedp.WaitVisible(".bookmark.is-collapsed .article-title a[aria-expanded=false]", chromedp.ByQuery),
			chromedp.Evaluate(`getComputedStyle(document.querySelector('.bookmark')).backgroundColor`, &after),
			chromedp.Evaluate(`document.documentElement.scrollWidth > innerWidth`, &overflow),
			chromedp.Location(&location),
			chromedp.Evaluate(`document.querySelector(".understood-button").textContent`, &label)); err != nil {
			t.Fatal(err)
		}
		if before == after || overflow || location != server.URL+"/mine" || label != "✓ 理解済み" {
			t.Fatalf("width=%d before=%s after=%s overflow=%t location=%s label=%s", width, before, after, overflow, location, label)
		}
		if err := chromedp.Run(ctx,
			chromedp.Reload(),
			chromedp.WaitVisible(".bookmark.is-collapsed .article-title a[aria-expanded=false]", chromedp.ByQuery),
			chromedp.Evaluate(`Array.from(document.querySelector('.bookmark').children).filter(el => el.checkVisibility()).every(el => el.classList.contains('article-title'))`, &titleOnly),
			chromedp.Click(".article-title a", chromedp.ByQuery),
			chromedp.WaitVisible(".understood-button", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('.article-title a').getAttribute('aria-expanded') === 'true' && document.querySelector('.bookmark').classList.contains('is-understood')`, &expanded),
			chromedp.Click(".understood-button", chromedp.ByQuery),
			chromedp.WaitVisible(".bookmark:not(.is-understood) .understood-button[aria-pressed=false]", chromedp.ByQuery),
			chromedp.Text(".understood-button", &label, chromedp.ByQuery)); err != nil {
			t.Fatal(err)
		}
		if label != "理解した" || !titleOnly || !expanded {
			t.Fatalf("undo label: %s", label)
		}
	}
}
