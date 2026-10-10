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

	"github.com/chromedp/chromedp"
)

// Exercise a real HTML form navigation: unlike fetch, it sends Origin: null
// under no-referrer, which the CSRF middleware correctly rejects.
func TestTokenIssueBrowserFormOrigin(t *testing.T) {
	chrome := ""
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			chrome = path
			break
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium is required for browser form regression")
	}
	a, _, _ := apiFixture(t)
	a.cfg.APIOnly = false
	a.cfg.Env = "development"
	type result struct {
		status int
		origin string
	}
	submitted := make(chan result, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Supply a test session without involving Google or a real user's cookies.
		r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "test-session"})
		response := httptest.NewRecorder()
		a.Handler().ServeHTTP(response, r)
		for name, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
		if r.Method == "POST" && r.URL.Path == "/settings/api/issue" {
			submitted <- result{response.Code, r.Header.Get("Origin")}
		}
	}))
	a.cfg.BaseURL = "http://" + server.Listener.Addr().String()
	server.Start()
	defer server.Close()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	if os.Getuid() == 0 {
		options = append(options, chromedp.NoSandbox)
	}
	allocator, cancel := chromedp.NewExecAllocator(context.Background(), options...)
	defer cancel()
	browser, cancel := chromedp.NewContext(allocator)
	defer cancel()
	ctx, cancel := context.WithTimeout(browser, 20*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+"/settings/api"), chromedp.Click(`form[action="/settings/api/issue"] button`, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-submitted:
		if got.status != http.StatusOK {
			t.Fatalf("browser form status=%d Origin=%q; want 200 and %q", got.status, got.origin, server.URL)
		}
		if got.origin != server.URL {
			t.Fatalf("Origin=%q, want %q", got.origin, server.URL)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var token, command, headerCommand string
	if err := chromedp.Run(ctx, chromedp.WaitVisible("#api-token", chromedp.ByQuery), chromedp.Value("#api-token", &token, chromedp.ByQuery), chromedp.Text("#api-command", &command, chromedp.ByQuery), chromedp.Text("#api-header-command", &headerCommand, chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	if len(token) != 108 || !strings.Contains(command, "?token="+token+"'") {
		t.Fatal("displayed token and curl do not match")
	}
	if !strings.Contains(headerCommand, "-H 'x-shiori-api: "+token+"'") || strings.Contains(headerCommand, "?token=") {
		t.Fatal("displayed header curl does not match token")
	}
	for _, method := range []string{"query", "header"} {
		t.Run(method, func(t *testing.T) {
			target := "/api/bookmarks"
			if method == "query" {
				target += "?token=" + url.QueryEscape(token)
			}
			r := httptest.NewRequest("POST", target, strings.NewReader(`{"url":"https://example.com/article"}`))
			r.Header.Set("Content-Type", "application/json")
			if method == "header" {
				r.Header.Set("x-shiori-api", token)
			}
			w := httptest.NewRecorder()
			a.Handler().ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("browser-issued token rejected: status=%d", w.Code)
			}
		})
	}
}
