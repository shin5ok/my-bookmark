package content

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
)

func TestDocumentImages(t *testing.T) {
	data := []byte(`<base href="https://cdn.example.com/assets/"><nav><img src="nav.png"></nav><article><p>売上推移を示す。</p><figure><img src="placeholder.gif" data-src="chart.png" alt="売上グラフ" width="800" height="400"><figcaption>図1: 2025年の売上</figcaption></figure><img src="chart.png"><img src="logo.png" alt="site logo"><img src="http://169.254.169.254/secret"><img src="pixel.png" width="1" height="1"></article><footer><img src="footer.png"></footer>`)
	images := DocumentImages(data, "text/html; charset=utf-8", "https://example.com/post")
	if len(images) != 1 || images[0].URL != "https://cdn.example.com/assets/chart.png" || !strings.Contains(images[0].Description, "2025年の売上") {
		t.Fatalf("unexpected candidates: %+v", images)
	}
}

func TestDocumentImagesJapaneseCharset(t *testing.T) {
	encoded, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(`<meta charset="shift_jis"><article><figure><img src="/chart.png" alt="売上グラフ"><figcaption>図1: 売上の増加</figcaption></figure></article>`))
	if err != nil {
		t.Fatal(err)
	}
	candidates := DocumentImages(encoded, "text/html", "https://example.com/post")
	if len(candidates) != 1 || !strings.Contains(candidates[0].Description, "売上の増加") {
		t.Fatalf("description: %+v", candidates)
	}
}

func TestFetchImageLimits(t *testing.T) {
	var valid bytes.Buffer
	png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 320, 200)))
	for _, tc := range []struct {
		name, url string
		data      []byte
		status    int
		wantErr   bool
	}{
		{"valid", "https://example.com/chart.png", valid.Bytes(), 200, false},
		{"HTML", "https://example.com/chart.png", []byte("<html>not an image</html>"), 200, true},
		{"broken PNG", "https://example.com/chart.png", []byte("\x89PNG\r\n\x1a\n"), 200, true},
		{"large", "https://example.com/chart.png", bytes.Repeat([]byte("x"), 4*1024*1024+1), 200, true},
		{"private", "http://127.0.0.1/image.png", valid.Bytes(), 200, true},
		{"status", "https://example.com/chart.png", valid.Bytes(), 403, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				w.WriteHeader(tc.status)
				w.Write(tc.data)
				return w.Result(), nil
			})}
			img, err := FetchImage(context.Background(), client, ImageCandidate{URL: tc.url})
			if (err != nil) != tc.wantErr {
				t.Fatalf("image=%+v err=%v", img, err)
			}
			if !tc.wantErr && (img.MIMEType != "image/png" || !bytes.Equal(img.Data, valid.Bytes())) {
				t.Fatal("image bytes lost")
			}
		})
	}
}

func TestDocumentImagesResponsiveSources(t *testing.T) {
	candidates := DocumentImages([]byte(`<article><picture><source type="image/webp" srcset="/small.webp 400w, /diagram.webp 1000w"><img src="data:image/gif;base64,placeholder" alt="構成図"></picture><img src="/fallback.png" srcset="/tiny.png 100w, /chart.png 800w" alt="売上グラフ"></article>`), "text/html; charset=utf-8", "https://example.com/post")
	if len(candidates) != 2 || candidates[0].URL != "https://example.com/diagram.webp" || candidates[1].URL != "https://example.com/chart.png" {
		t.Fatalf("candidates: %+v", candidates)
	}
}
