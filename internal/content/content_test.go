package content

import (
	"net/netip"
	"testing"
)

func TestNormalizeURL(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://localhost/", "http://127.0.0.1", "https://user:pass@example.com", "http://[::1]", "http://example.com:8080"} {
		if _, err := NormalizeURL(raw); err == nil {
			t.Errorf("accepted unsafe URL %q", raw)
		}
	}
	got, err := NormalizeURL("HTTPS://Example.COM/a?q=1#section")
	if err != nil || got != "https://example.com/a?q=1" {
		t.Fatalf("got %q, %v", got, err)
	}
}
func TestPublicIP(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.0.0.1", "::1", "fc00::1", "::ffff:127.0.0.1", "2001:db8::1"} {
		if PublicIP(netip.MustParseAddr(ip)) {
			t.Errorf("accepted %s", ip)
		}
	}
	if !PublicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
}
func TestValidateSummary(t *testing.T) {
	for _, points := range [][]string{nil, {""}, {"a", "b", "c", "d", "e", "f"}} {
		if err := ValidateSummary(points); err == nil {
			t.Errorf("accepted %#v", points)
		}
	}
	if err := ValidateSummary([]string{"概要", "重要なポイント"}); err != nil {
		t.Fatal(err)
	}
}
func TestExtract(t *testing.T) {
	title, body, err := Extract([]byte(`<html><head><title>記事 &amp; 情報</title></head><body><nav>メニュー</nav><article><h1>見出し</h1><p>本文です。</p><script>secret()</script></article></body></html>`), "text/html; charset=utf-8")
	if err != nil || title != "記事 & 情報" || body != "見出し 本文です。" {
		t.Fatalf("%q %q %v", title, body, err)
	}
}
