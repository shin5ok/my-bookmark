package content

import (
	"bytes"
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const MaxSummaryImages = 3
const maxImageBytes = 4 * 1024 * 1024

type ImageCandidate struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}
type Image struct {
	ImageCandidate
	MIMEType string
	Data     []byte
}

// DocumentImages collects a bounded set of article images, excluding navigation,
// decorations and tracking pixels. Selection by relevance happens before download.
func DocumentImages(data []byte, contentType, base string) []ImageCandidate {
	reader, err := charset.NewReader(bytes.NewReader(data), contentType)
	if err != nil {
		return nil
	}
	root, err := html.Parse(reader)
	if err != nil {
		return nil
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil
	}
	var article, main, body *html.Node
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "base":
				if ref, e := url.Parse(imageAttr(n, "href")); e == nil {
					if u, e := NormalizeURL(baseURL.ResolveReference(ref).String()); e == nil {
						baseURL, _ = url.Parse(u)
					}
				}
			case "article":
				if article == nil {
					article = n
				}
			case "main":
				if main == nil {
					main = n
				}
			case "body":
				body = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			scan(c)
		}
	}
	scan(root)
	target := root
	if body != nil {
		target = body
	}
	if main != nil {
		target = main
	}
	if article != nil {
		target = article
	}
	type ranked struct {
		candidate ImageCandidate
		score     int
	}
	var candidates []ranked
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "nav", "header", "footer", "aside", "script", "style", "form":
				return
			case "img":
				if len(candidates) >= 100 {
					return
				}
				raw := imageSource(n)
				ref, e := url.Parse(raw)
				if e != nil || raw == "" {
					return
				}
				normalized, e := NormalizeURL(baseURL.ResolveReference(ref).String())
				if e != nil || seen[normalized] {
					return
				}
				label := strings.TrimSpace(imageAttr(n, "alt") + " " + imageAttr(n, "title"))
				decoration := strings.ToLower(label + " " + normalized + " " + imageAttr(n, "class"))
				for _, term := range []string{"logo", "avatar", "icon", "banner", "tracking", "広告", "ロゴ", "アバター"} {
					if strings.Contains(decoration, term) {
						return
					}
				}
				for _, key := range []string{"width", "height"} {
					if size, e := strconv.Atoi(imageAttr(n, key)); e == nil && size > 0 && size < 80 {
						return
					}
				}
				if imageAttr(n, "aria-hidden") == "true" {
					return
				}
				context := n.Parent
				for p := n.Parent; p != nil && p != target; p = p.Parent {
					if p.Data == "figure" {
						context = p
						break
					}
				}
				description := truncate(strings.TrimSpace(label+" "+nodeText(context)), 600)
				score := 0
				if label != "" {
					score++
				}
				if context != nil && context.Data == "figure" {
					score += 3
				}
				for _, term := range []string{"chart", "graph", "diagram", "table", "figure", "グラフ", "図", "表", "構成", "手順"} {
					if strings.Contains(strings.ToLower(description+" "+normalized), term) {
						score += 4
						break
					}
				}
				seen[normalized] = true
				if len(candidates) < 100 {
					candidates = append(candidates, ranked{ImageCandidate{normalized, description}, score})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(target)
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })
	if len(candidates) > 20 {
		candidates = candidates[:20]
	}
	out := make([]ImageCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.candidate)
	}
	return out
}

func imageAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}
func imageSource(n *html.Node) string {
	for _, key := range []string{"data-src", "data-original", "data-lazy-src"} {
		if v := imageAttr(n, key); v != "" {
			return v
		}
	}
	if n.Data == "img" && n.Parent != nil && n.Parent.Data == "picture" {
		for c := n.Parent.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "source" {
				mime := imageAttr(c, "type")
				if mime == "" || mime == "image/webp" || mime == "image/png" || mime == "image/jpeg" {
					if source := imageSource(c); source != "" {
						return source
					}
				}
			}
		}
	}
	for _, key := range []string{"data-srcset", "srcset"} {
		if v := imageAttr(n, key); v != "" {
			entries := strings.Split(v, ",")
			fields := strings.Fields(entries[len(entries)-1])
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return imageAttr(n, "src")
}

// FetchImage uses the same DNS and redirect checks as article fetching. MIME is
// detected from bytes rather than trusting the page's URL or response headers.
func FetchImage(parent context.Context, client *http.Client, candidate ImageCandidate) (Image, error) {
	normalized, err := NormalizeURL(candidate.URL)
	if err != nil {
		return Image{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return Image{}, err
	}
	req.Header.Set("User-Agent", "ShioriBookmark/1.0 (article summary)")
	req.Header.Set("Accept", "image/jpeg, image/png, image/webp")
	res, err := client.Do(req)
	if err != nil {
		return Image{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Image{}, errors.New("image returned non-200 status")
	}
	if res.ContentLength > maxImageBytes {
		return Image{}, errors.New("image too large")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxImageBytes+1))
	if err != nil {
		return Image{}, err
	}
	if len(data) > maxImageBytes {
		return Image{}, errors.New("image too large")
	}
	mime := http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return Image{}, errors.New("unsupported image type")
	}
	if mime == "image/jpeg" || mime == "image/png" {
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width < 80 || config.Height < 80 || int64(config.Width)*int64(config.Height) > 40_000_000 {
			return Image{}, errors.New("invalid image dimensions or encoding")
		}
	}
	return Image{ImageCandidate: candidate, MIMEType: mime, Data: data}, nil
}
