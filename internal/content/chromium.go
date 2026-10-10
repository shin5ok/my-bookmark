package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"golang.org/x/net/html"
)

type ChromeRenderer struct{ timeout time.Duration }

func NewChromeRenderer() *ChromeRenderer { return &ChromeRenderer{timeout: 15 * time.Second} }

// Check verifies browser startup and script execution before accepting summary work.
func (r *ChromeRenderer) Check(ctx context.Context) error {
	if os.Getuid() == 0 {
		return errors.New("sandboxed Chromium must run as a non-root user")
	}
	doc, err := r.Render(ctx, []byte(`<html><head><title>Sandbox startup check</title></head><body><script>document.body.textContent = "sandbox-render-ok";</script></body></html>`), "https://example.com/")
	if err != nil {
		return fmt.Errorf("start sandboxed Chromium: %w", err)
	}
	if doc.Title != "Sandbox startup check" || doc.Text != "sandbox-render-ok" {
		return errors.New("sandboxed Chromium startup check did not render the expected content")
	}
	return nil
}

// Render executes inline page scripts in an isolated blank page. The only network path
// offered to page code is an intentionally dead proxy; links are fetched by our Go client.
func (r *ChromeRenderer) Render(parent context.Context, source []byte, base string) (Document, error) {
	ctx, cancel := context.WithTimeout(parent, r.timeout)
	defer cancel()
	options := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("no-sandbox", false), chromedp.Flag("disable-gpu", true), chromedp.Flag("disable-background-networking", true), chromedp.Flag("disable-extensions", true), chromedp.Flag("disable-sync", true), chromedp.Flag("disable-dev-shm-usage", true), chromedp.Flag("dns-prefetch-disable", true), chromedp.Flag("force-webrtc-ip-handling-policy", "disable_non_proxied_udp"), chromedp.Flag("disable-component-update", true), chromedp.Flag("no-first-run", true), chromedp.Flag("proxy-server", "http://127.0.0.1:9"), chromedp.Flag("proxy-bypass-list", "<-loopback>"),
	)
	if path := os.Getenv("CHROME_BIN"); path != "" {
		options = append(options, chromedp.ExecPath(path))
	}
	alloc, allocCancel := chromedp.NewExecAllocator(ctx, options...)
	defer allocCancel()
	browser, browserCancel := chromedp.NewContext(alloc)
	defer browserCancel()
	var tree *page.FrameTree
	if err := chromedp.Run(browser, chromedp.Navigate("about:blank"), chromedp.ActionFunc(func(ctx context.Context) error { var err error; tree, err = page.GetFrameTree().Do(ctx); return err })); err != nil {
		return Document{}, err
	}
	if tree == nil || tree.Frame == nil {
		return Document{}, errors.New("browser frame unavailable")
	}
	parsed, err := html.Parse(strings.NewReader(string(source)))
	if err != nil {
		return Document{}, err
	}
	baseNode := &html.Node{Type: html.ElementNode, Data: "base", Attr: []html.Attribute{{Key: "href", Val: base}}}
	var head *html.Node
	var findHead func(*html.Node)
	findHead = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "head" && head == nil {
			head = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findHead(c)
		}
	}
	findHead(parsed)
	if head != nil {
		head.InsertBefore(baseNode, head.FirstChild)
	} else {
		parsed.AppendChild(baseNode)
	}
	var sourceHTML strings.Builder
	if err = html.Render(&sourceHTML, parsed); err != nil {
		return Document{}, err
	}
	if err = chromedp.Run(browser, page.SetDocumentContent(tree.Frame.ID, sourceHTML.String())); err != nil {
		return Document{}, err
	}
	timer := time.NewTimer(1200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Document{}, ctx.Err()
	case <-timer.C:
	}
	script := `JSON.stringify({title:document.title,text:document.body?document.body.innerText:"",html:document.documentElement?document.documentElement.outerHTML:""})`
	var encoded string
	if err = chromedp.Run(browser, chromedp.Evaluate(script, &encoded)); err != nil {
		return Document{}, err
	}
	var value struct {
		Title string `json:"title"`
		Text  string `json:"text"`
		HTML  string `json:"html"`
	}
	if err = json.Unmarshal([]byte(encoded), &value); err != nil {
		return Document{}, err
	}
	if len(value.HTML) > 2*1024*1024 || len([]rune(value.Text)) > 24000 {
		return Document{}, errors.New("rendered page too large")
	}
	title, links, err := DocumentLinks([]byte(value.HTML), "text/html; charset=utf-8", base)
	if err != nil {
		return Document{}, err
	}
	if value.Title != "" {
		title = value.Title
	}
	return Document{URL: base, Title: truncate(title, 240), Text: truncate(strings.Join(strings.Fields(value.Text), " "), 24000), HTML: []byte(value.HTML), Links: links, ContentType: "text/html; charset=utf-8"}, nil
}
