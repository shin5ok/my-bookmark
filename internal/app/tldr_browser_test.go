package app

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"my-bookmark/internal/store"
)

func TestTLDRPollingAndAccordionInBrowser(t *testing.T) {
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
	id := strings.Repeat("c", 64)
	ready := store.Article{ID: id, URL: "https://example.com/", Title: "具体的な日本語タイトル", Status: "ready", Points: []string{"具体的な本文です。数値や手順を説明します。"}, TLDR: []string{"記事の結論を短く伝えます。", "重要な理由を説明します。", "読者への影響をまとめます。"}}
	pending := ready
	pending.Title = "example.com"
	pending.Status = "queued"
	pending.Points = nil
	pending.TLDR = nil
	a := testApp(t)
	a.db = &summaryTestDB{testDB: testDB{entries: []store.Entry{{Article: pending}}}, article: ready}
	server := httptest.NewServer(a.Handler())
	defer server.Close()
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
		var visible, overflow bool
		var count int
		var title, location string
		if err := chromedp.Run(ctx, chromedp.EmulateViewport(width, 900), chromedp.Navigate(server.URL), chromedp.WaitVisible(".tldr", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelectorAll('.tldr li').length`, &count),
			chromedp.Evaluate(`document.querySelector('.summary-content ul').checkVisibility()`, &visible),
			chromedp.Evaluate(`document.documentElement.scrollWidth > window.innerWidth`, &overflow),
			chromedp.Text(".article-title", &title, chromedp.ByQuery)); err != nil {
			t.Fatal(err)
		}
		if count != 3 || visible || overflow || title != ready.Title {
			t.Fatalf("width=%d lines=%d full=%t overflow=%t title=%q", width, count, visible, overflow, title)
		}
		if err := chromedp.Run(ctx, chromedp.Click(".summary-disclosure > summary", chromedp.ByQuery), chromedp.Location(&location), chromedp.Evaluate(`document.querySelector('.summary-content ul').checkVisibility()`, &visible)); err != nil {
			t.Fatal(err)
		}
		if !visible || location != server.URL+"/" {
			t.Fatalf("accordion navigated or did not open: %t %s", visible, location)
		}
		if err := chromedp.Run(ctx, chromedp.Click(".summary-disclosure > summary", chromedp.ByQuery), chromedp.Evaluate(`document.querySelector('.summary-content ul').checkVisibility()`, &visible)); err != nil {
			t.Fatal(err)
		}
		if visible {
			t.Fatal("accordion did not close")
		}
	}
}
