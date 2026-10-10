package content

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChromeRendererNeverDisablesSandbox(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	browser := filepath.Join(dir, "chrome")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s\\n' invocation \"$@\" >> \"$CHROME_TEST_ARGS\"\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHROME_BIN", browser)
	t.Setenv("CHROME_TEST_ARGS", argsFile)
	_, err := NewChromeRenderer().Render(context.Background(), []byte("<html><body>test</body></html>"), "https://example.com/")
	if err == nil {
		t.Fatal("failed browser must return an error")
	}
	data, readErr := os.ReadFile(argsFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Count(string(data), "invocation\n") != 1 {
		t.Fatal("browser startup failure must not trigger another launch")
	}
	for _, arg := range strings.Fields(string(data)) {
		for _, forbidden := range []string{"--no-sandbox", "--disable-setuid-sandbox", "--disable-seccomp-filter-sandbox", "--disable-namespace-sandbox"} {
			if arg == forbidden || strings.HasPrefix(arg, forbidden+"=") {
				t.Fatalf("sandbox disabled by %s", arg)
			}
		}
	}
	if !strings.Contains(string(data), "--proxy-server=http://127.0.0.1:9") || !strings.Contains(string(data), "--proxy-bypass-list=<-loopback>") {
		t.Fatal("renderer network restrictions missing")
	}
}

func TestChromeRendererStartupCheck(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("sandboxed Chrome requires a non-root user")
	}
	chrome := os.Getenv("CHROME_BIN")
	if chrome == "" {
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"} {
			if path, err := exec.LookPath(name); err == nil {
				chrome = path
				break
			}
		}
	}
	if chrome == "" {
		t.Skip("Chrome/Chromium is required for sandbox startup check")
	}
	t.Setenv("CHROME_BIN", chrome)
	if err := NewChromeRenderer().Check(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestChromeRendererStartupCheckRejectsUnavailableBrowser(t *testing.T) {
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "missing-chrome"))
	if err := NewChromeRenderer().Check(context.Background()); err == nil {
		t.Fatal("unavailable browser passed startup check")
	}
}
