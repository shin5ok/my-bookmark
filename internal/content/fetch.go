package content

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("URLを確認してください")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	if (u.Scheme != "http" && u.Scheme != "https") || host == "" || u.User != nil || len(raw) > 2048 {
		return "", errors.New("公開されたHTTP/HTTPSのURLを入力してください")
	}
	if strings.HasSuffix(host, ".") || host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "", errors.New("公開URLのみ保存できます")
	}
	if ip, err := netip.ParseAddr(host); err == nil && !PublicIP(ip) {
		return "", errors.New("内部アドレスは利用できません")
	}
	port := u.Port()
	if port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		return "", errors.New("標準ポートのみ利用できます")
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

var blocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"), netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
}

func PublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, p := range blocked {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

func NewFetcher() *http.Client {
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("no addresses")
			}
			for _, ip := range ips {
				if !PublicIP(ip) {
					return nil, errors.New("non-public address blocked")
				}
			}
			// Connect to the validated IP, never resolve the hostname a second time.
			var last error
			for _, ip := range ips {
				c, e := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					return c, nil
				}
				last = e
			}
			return nil, last
		},
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		_, err := NormalizeURL(req.URL.String())
		return err
	}}
}

func Fetch(ctx context.Context, client *http.Client, raw string) (string, string, error) {
	normalized, err := NormalizeURL(raw)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "ShioriBookmark/1.0 (article summary)")
	req.Header.Set("Accept", "text/html, text/plain;q=0.8")
	res, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("article returned %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2*1024*1024+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > 2*1024*1024 {
		return "", "", errors.New("article too large")
	}
	title, body, err := Extract(data, res.Header.Get("Content-Type"))
	if err == nil && len([]rune(body)) < 40 {
		return "", "", errors.New("article text too short")
	}
	return title, body, err
}
func Extract(data []byte, contentType string) (string, string, error) {
	if strings.HasPrefix(contentType, "text/plain") {
		return "", truncate(strings.Join(strings.Fields(string(data)), " "), 24000), nil
	}
	if !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "application/xhtml+xml") {
		return "", "", errors.New("HTML記事のみ対応しています")
	}
	reader, err := charset.NewReader(strings.NewReader(string(data)), contentType)
	if err != nil {
		return "", "", err
	}
	root, err := html.Parse(reader)
	if err != nil {
		return "", "", err
	}
	var title string
	var article, main, body *html.Node
	var scan func(*html.Node)
	scan = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "title":
				title = nodeText(n)
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
	return truncate(title, 240), truncate(nodeText(target), 24000), nil
}
func nodeText(n *html.Node) string {
	var chunks []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script", "style", "nav", "footer", "header", "aside", "noscript", "svg", "form":
				return
			}
		}
		if n.Type == html.TextNode {
			chunks = append(chunks, n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(strings.Join(chunks, " ")), " ")
}
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
func ValidateSummary(points []string) error {
	if len(points) < 1 || len(points) > 5 {
		return errors.New("要約は1〜5項目である必要があります")
	}
	for _, p := range points {
		if strings.TrimSpace(p) == "" || utf8.RuneCountInString(p) > 240 {
			return errors.New("要約の項目が空、または長すぎます")
		}
	}
	return nil
}

// TruncateText bounds text passed to the model and kept in temporary job memory.
func TruncateText(s string, n int) string { return truncate(s, n) }
