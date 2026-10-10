package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"my-bookmark/internal/store"
)

func TestCustomSummaryInBrowser(t *testing.T) {
	chrome := ""
	for _, name := range []string{"chromium", "google-chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			chrome = path
			break
		}
	}
	if chrome == "" {
		t.Skip("Chrome required")
	}
	id := strings.Repeat("a", 64)
	ready := store.Article{ID: id, URL: "https://example.com/", Title: "追加指示で要約をやり直す", Status: "ready", Points: []string{"元の記事から作った要約です。"}, TLDR: []string{"記事の結論。", "重要な理由。"}}
	a := testApp(t)
	a.db = &summaryTestDB{owned: true, article: ready}
	posts := make(chan string, 8)
	finished := ready
	finished.Points = []string{"one", "two", "three", "four", "five", "six", "seven"}
	finished.TLDR = []string{"One sentence."}
	queued := ready
	queued.Status = "queued"
	handler := a.Handler()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/summary") {
			posts <- r.PostFormValue("style") + ":" + r.PostFormValue("instruction")
			writeJSON(w, 202, summaryData(queued))
			return
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/summary") {
			writeJSON(w, 200, summaryData(finished))
			return
		}
		r.AddCookie(&http.Cookie{Name: "shiori_session", Value: "token"})
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	options := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.ExecPath(chrome))
	alloc, stop := chromedp.NewExecAllocator(context.Background(), options...)
	defer stop()
	browser, stop := chromedp.NewContext(alloc)
	defer stop()
	ctx, cancel := context.WithTimeout(browser, 30*time.Second)
	defer cancel()
	for _, viewport := range []struct {
		width int64
		name  string
	}{{1280, "desktop"}, {390, "mobile"}} {
		var overflow bool
		var width float64
		var screenshot []byte
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(viewport.width, 900), chromedp.Navigate(server.URL+"/articles/"+id), chromedp.Click(".summary-disclosure > summary", chromedp.ByQuery),
			chromedp.Evaluate(`document.documentElement.scrollWidth > innerWidth`, &overflow),
			chromedp.Evaluate(`document.querySelector('.summary-instruction').getBoundingClientRect().width`, &width),
			chromedp.FullScreenshot(&screenshot, 90)); err != nil {
			t.Fatal(err)
		}
		if overflow || width > 230 {
			t.Fatalf("%s overflow=%v input width=%g", viewport.name, overflow, width)
		}
		if err := os.WriteFile("/tmp/shiori-summary-custom-"+viewport.name+".png", screenshot, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// The browser counts Unicode code points, matching the server (including emoji).
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => { const input=document.querySelector('.summary-instruction'); input.value='詳'.repeat(201); input.dispatchEvent(new Event('input')); })()`, nil), chromedp.Click(".summary-custom-submit", chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-posts:
		t.Fatalf("overlong input submitted: %q", got)
	default:
	}
	var invalid bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`!document.querySelector('.summary-instruction').checkValidity()`, &invalid)); err != nil {
		t.Fatal(err)
	}
	if !invalid {
		t.Fatal("201 characters accepted")
	}
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {const input=document.querySelector('.summary-instruction'); input.value='😀'.repeat(200); input.dispatchEvent(new Event('input')); input.focus();})()`, nil), chromedp.KeyEvent(kb.Enter), chromedp.Poll(`document.querySelector('.summary-instruction').value === '' && document.querySelector('.summary').dataset.status === 'ready'`, nil)); err != nil {
		t.Fatal(err)
	}
	var points, lines int
	if err := chromedp.Run(ctx, chromedp.Evaluate(`document.querySelectorAll('.summary-content li').length`, &points), chromedp.Evaluate(`document.querySelectorAll('.tldr li').length`, &lines)); err != nil {
		t.Fatal(err)
	}
	if points != 7 || lines != 1 {
		t.Fatalf("custom output clipped: points=%d TLDR=%d", points, lines)
	}
	select {
	case got := <-posts:
		if got != "concrete:"+strings.Repeat("😀", 200) {
			t.Fatalf("wrong submitted instruction %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := chromedp.Run(ctx, chromedp.SendKeys(".summary-instruction", "英語で7項目", chromedp.ByQuery), chromedp.Click(`[data-style="simple"]`, chromedp.ByQuery), chromedp.Poll(`document.querySelector('.summary-instruction').value === '' && document.querySelector('.summary').dataset.status === 'ready'`, nil)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-posts:
		if got != "simple:英語で7項目" {
			t.Fatalf("style did not include instruction %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := chromedp.Run(ctx, chromedp.Click(".summary-custom-submit", chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-posts:
		if got != "concrete:" {
			t.Fatalf("previous instruction reused %q", got)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
