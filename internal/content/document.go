package content

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

type Link struct {
	URL   string
	Text  string
	Score int
}
type Document struct {
	URL         string
	Title       string
	Text        string
	HTML        []byte
	Links       []Link
	ContentType string
}

// FetchDocument performs one SSRF-checked request and returns document-relative links.
// Short documents are valid here so the renderer and linked-page fallback can inspect them.
func FetchDocument(ctx context.Context, client *http.Client, raw string) (Document, error) {
	normalized, err := NormalizeURL(raw)
	if err != nil {
		return Document{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return Document{}, err
	}
	req.Header.Set("User-Agent", "ShioriBookmark/1.0 (article summary)")
	req.Header.Set("Accept", "text/html, text/plain;q=0.8")
	res, err := client.Do(req)
	if err != nil {
		return Document{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Document{}, errors.New("article returned non-200 status")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return Document{}, err
	}
	if len(data) > 2*1024*1024 {
		return Document{}, errors.New("article too large")
	}
	ctype := res.Header.Get("Content-Type")
	if !strings.Contains(ctype, "text/html") && !strings.Contains(ctype, "application/xhtml+xml") {
		return Document{}, errors.New("article is not HTML")
	}
	finalURL := normalized
	if res.Request != nil && res.Request.URL != nil {
		finalURL = res.Request.URL.String()
	}
	title, text, err := Extract(data, ctype)
	if err != nil {
		return Document{}, err
	}
	_, links, err := DocumentLinks(data, ctype, finalURL)
	if err != nil {
		return Document{}, err
	}
	return Document{URL: finalURL, Title: title, Text: text, HTML: data, Links: links, ContentType: ctype}, nil
}

// DocumentLinks extracts direct anchors from the HTML and ranks likely article links.
func DocumentLinks(data []byte, contentType, base string) (string, []Link, error) {
	reader, err := charset.NewReader(strings.NewReader(string(data)), contentType)
	if err != nil {
		return "", nil, err
	}
	root, err := html.Parse(reader)
	if err != nil {
		return "", nil, err
	}
	parsed, err := url.Parse(base)
	if err != nil {
		return "", nil, err
	}
	title := ""
	baseURL := parsed
	var links []Link
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "title" && title == "" {
			title = nodeText(n)
		}
		if n.Type == html.ElementNode && n.Data == "base" {
			for _, a := range n.Attr {
				if a.Key == "href" {
					if ref, e := url.Parse(strings.TrimSpace(a.Val)); e == nil {
						candidate := parsed.ResolveReference(ref)
						if normalized, e := NormalizeURL(candidate.String()); e == nil {
							baseURL, _ = url.Parse(normalized)
						}
					}
					break
				}
			}
		}
		if n.Type == html.ElementNode && n.Data == "a" {
			var href string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = strings.TrimSpace(a.Val)
					break
				}
			}
			if href != "" && len(links) < 120 {
				if ref, e := url.Parse(href); e == nil {
					candidate := baseURL.ResolveReference(ref)
					if normalized, e := NormalizeURL(candidate.String()); e == nil && normalized != parsed.String() && !seen[normalized] {
						seen[normalized] = true
						text := nodeText(n)
						score := linkScore(text, candidate, parsed)
						if score > 0 {
							if len([]rune(text)) > 180 {
								text = string([]rune(text)[:180])
							}
							links = append(links, Link{URL: normalized, Text: text, Score: score})
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	sort.SliceStable(links, func(i, j int) bool { return links[i].Score > links[j].Score })
	if len(links) > 20 {
		links = links[:20]
	}
	return truncate(title, 240), links, nil
}
func linkScore(text string, u, base *url.URL) int {
	path := strings.ToLower(u.Path)
	label := strings.ToLower(text)
	score := 1
	if u.Hostname() == base.Hostname() {
		score += 4
	} else {
		score += 1
	}
	for _, term := range []string{"article", "story", "news", "post", "paper", "report", "docs", "guide", "blog", "reference", "論文", "記事", "解説", "詳しく", "続きを読む", "公式"} {
		if strings.Contains(path, term) || strings.Contains(label, term) {
			score += 3
			break
		}
	}
	for _, term := range []string{"privacy", "terms", "login", "signup", "share", "facebook", "twitter", "youtube", "instagram", "利用規約", "プライバシー", "ログイン", "会員登録", "シェア"} {
		if strings.Contains(path, term) || strings.Contains(label, term) {
			return 0
		}
	}
	if u.Hostname() == "" {
		return 0
	}
	return score
}

type Renderer interface {
	Render(context.Context, []byte, string) (Document, error)
}
