package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"golang.org/x/oauth2"
	"my-bookmark/internal/content"
	"my-bookmark/internal/store"
)

// Exercise HTTP save -> durable job -> task delivery -> actual Gemini request
// encoding -> Firestore result -> HTTP feed. Only external article/model HTTP
// responses are fixtures; the application's handlers and store remain real.
func TestImageSummaryFunctionalFlow(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore Emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := firestore.NewClient(ctx, "demo-image-flow-"+store.Hash(store.RandomToken())[:12])
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	db := store.New(client)
	images := imageFlowFixtures(t)
	for _, tc := range []struct {
		name, selection           string
		selectionStatus           int
		imageStatus               int
		failedPath                string
		shortText                 bool
		redirectPrivate           bool
		wantFetched, wantAttached []string
	}{
		{name: "one important image", selection: `{"images":[1]}`, wantFetched: []string{"/chart1.png"}, wantAttached: []string{"/chart1.png"}},
		{name: "short text with informative image", shortText: true, selection: `{"images":[1]}`, wantFetched: []string{"/chart1.png"}, wantAttached: []string{"/chart1.png"}},
		{name: "three independent images", selection: `{"images":[2,0,1]}`, wantFetched: []string{"/chart2.png", "/chart0.png", "/chart1.png"}, wantAttached: []string{"/chart2.png", "/chart0.png", "/chart1.png"}},
		{name: "no useful images", selection: `{"images":[]}`},
		{name: "failed image uses text", selection: `{"images":[0]}`, imageStatus: 403, wantFetched: []string{"/chart0.png"}},
		{name: "one failed image preserves other image", selection: `{"images":[0,1]}`, imageStatus: 403, failedPath: "/chart0.png", wantFetched: []string{"/chart0.png", "/chart1.png"}, wantAttached: []string{"/chart1.png"}},
		{name: "selection outage uses text", selectionStatus: 429},
		{name: "four selected images rejected", selection: `{"images":[0,1,2,3]}`},
		{name: "invented index rejected", selection: `{"images":[99]}`},
		{name: "duplicate selection rejected", selection: `{"images":[0,0]}`},
		{name: "private redirect blocked", selection: `{"images":[0]}`, redirectPrivate: true, wantFetched: []string{"/chart0.png"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantTitle, wantPoint := "本文からまとめた要約", "本文の手順を確認できます。"
			if len(tc.wantAttached) > 0 {
				wantTitle, wantPoint = "重要画像を活かした要約", "図表では売上が前年比20%増加しています。"
			}
			wantTLDR := []string{wantPoint, "詳しい根拠を要約にまとめました。"}
			var mu sync.Mutex
			var fetched []string
			modelCalls := 0
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/article/"):
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					repeats := 80
					if tc.shortText {
						repeats = 1
					}
					fmt.Fprint(w, `<html><title>元の売上記事</title><nav><img src="/navigation.png"></nav><article><p>`+strings.Repeat("記事本文では売上の計測方法と確認手順を説明しています。", repeats)+`</p><img src="/logo.png" alt="サイトのロゴ"><img src="http://169.254.169.254/secret.png">`)
					for i := 0; i < 4; i++ {
						fmt.Fprintf(w, `<figure><img data-src="/chart%d.png" src="/placeholder.gif" alt="売上グラフ%d"><figcaption>図%d: 売上推移</figcaption></figure>`, i, i, i)
					}
					fmt.Fprint(w, `</article></html>`)
				case strings.HasSuffix(r.URL.Path, ".png"):
					mu.Lock()
					fetched = append(fetched, r.URL.Path)
					mu.Unlock()
					if tc.redirectPrivate {
						http.Redirect(w, r, "http://169.254.169.254/secret.png", http.StatusFound)
						return
					}
					if tc.imageStatus != 0 && (tc.failedPath == "" || tc.failedPath == r.URL.Path) {
						w.WriteHeader(tc.imageStatus)
						return
					}
					data, ok := images[r.URL.Path]
					if !ok {
						t.Errorf("decoration or unknown image fetched: %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "image/png")
					w.Write(data)
				case strings.HasSuffix(r.URL.Path, ":generateContent"):
					mu.Lock()
					modelCalls++
					mu.Unlock()
					var request struct {
						GenerationConfig struct {
							ResponseJSONSchema struct{ Properties map[string]json.RawMessage }
						}
						Contents []struct {
							Parts []struct {
								Text       string
								InlineData *struct {
									MIMEType string `json:"mimeType"`
									Data     string
								}
							}
						}
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						http.Error(w, "bad JSON", 400)
						return
					}
					if r.Header.Get("Authorization") != "Bearer fixture-token" {
						t.Error("missing model authentication")
					}
					if _, selection := request.GenerationConfig.ResponseJSONSchema.Properties["images"]; selection {
						if tc.selectionStatus != 0 {
							w.WriteHeader(tc.selectionStatus)
							return
						}
						writeImageFlowModelResponse(w, tc.selection)
						return
					}
					if len(request.Contents) != 1 {
						t.Error("unexpected model contents")
						http.Error(w, "invalid contents", 400)
						return
					}
					var attached [][]byte
					for _, part := range request.Contents[0].Parts {
						if part.InlineData == nil {
							continue
						}
						if part.InlineData.MIMEType != "image/png" {
							t.Errorf("MIME type=%s", part.InlineData.MIMEType)
						}
						data, err := base64.StdEncoding.DecodeString(part.InlineData.Data)
						if err != nil {
							t.Error(err)
						}
						attached = append(attached, data)
					}
					if len(attached) != len(tc.wantAttached) {
						t.Errorf("attached %d images, want %d", len(attached), len(tc.wantAttached))
					}
					for i, path := range tc.wantAttached {
						if i < len(attached) && !bytes.Equal(attached[i], images[path]) {
							t.Errorf("wrong image at index %d; want %s", i, path)
						}
					}
					if len(request.Contents[0].Parts) == 0 || !strings.Contains(request.Contents[0].Parts[0].Text, "売上の計測方法") {
						t.Error("article text missing from final summary request")
					}
					output, _ := json.Marshal(map[string]any{"sufficient": true, "title": wantTitle, "points": []string{wantPoint}, "tldr": wantTLDR})
					writeImageFlowModelResponse(w, string(output))
				default:
					t.Errorf("unexpected fixture request: %s", r.URL.String())
					http.NotFound(w, r)
				}
			}))
			defer fixture.Close()
			gemini := &content.Gemini{Project: "fixture-project", Location: "global", Model: "fixture-model", Endpoint: fixture.URL, Client: fixture.Client(), TokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "fixture-token"})}
			app, err := New(Config{BaseURL: "http://localhost:8080", Env: "development", GeminiModel: "fixture-model"}, db, gemini)
			if err != nil {
				t.Fatal(err)
			}
			token, csrf := store.RandomToken(), store.RandomToken()
			user := store.User{ID: store.Hash(store.RandomToken()), Name: "image reader"}
			if err := db.PutSession(ctx, token, store.Session{User: user, CSRF: csrf, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			defer db.DeleteSession(ctx, token)
			request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, path, strings.NewReader(values.Encode())).WithContext(ctx)
				r.AddCookie(&http.Cookie{Name: "shiori_session", Value: token})
				if method == http.MethodPost {
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				}
				w := httptest.NewRecorder()
				app.Handler().ServeHTTP(w, r)
				return w
			}
			raw := "https://images.example/article/" + store.RandomToken()
			id := store.Hash(raw)
			saved := request(http.MethodPost, "/bookmarks", url.Values{"csrf": {csrf}, "url": {raw}})
			if saved.Code != http.StatusSeeOther || saved.Header().Get("Location") != "/articles/"+id {
				t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
			}
			defer db.Delete(ctx, user.ID, id)
			jobs, err := db.QueuedJobs(ctx)
			if err != nil || len(jobs) != 1 || jobs[0].ArticleID != id {
				t.Fatalf("queued jobs=%+v err=%v", jobs, err)
			}
			if err := db.MarkDispatched(ctx, id, jobs[0].QueuedAt); err != nil {
				t.Fatal(err)
			}
			worker := NewWorker(db, gemini, "fixture-model")
			// Redirect only the test host to our local fixture. Keep the production
			// HTTP client's redirect validation to exercise private redirect blocking.
			base, _ := url.Parse(fixture.URL)
			worker.fetcher.Transport = imageFlowTransport{base: base, transport: fixture.Client().Transport, unexpectedHost: func(host string) {
				t.Errorf("unsafe request reached the transport: %s", host)
			}}
			taskBody, _ := json.Marshal(TaskRequest{ArticleID: id, QueuedAt: jobs[0].QueuedAt})
			deliver := func() *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/tasks/summary", bytes.NewReader(taskBody)).WithContext(ctx)
				w := httptest.NewRecorder()
				TaskHandler(db, worker).ServeHTTP(w, r)
				return w
			}
			if task := deliver(); task.Code != http.StatusNoContent {
				t.Fatalf("task: %d %s", task.Code, task.Body.String())
			}
			article, err := db.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			wantSources := []string{raw}
			for _, path := range tc.wantAttached {
				wantSources = append(wantSources, "https://images.example"+path)
			}
			if article.Status != "ready" || article.LastError != "" || article.Title != wantTitle || !reflect.DeepEqual(article.Points, []string{wantPoint}) || !reflect.DeepEqual(article.TLDR, wantTLDR) || !reflect.DeepEqual(article.Sources, wantSources) {
				t.Fatalf("saved result: %+v", article)
			}
			status := request(http.MethodGet, "/articles/"+id+"/summary", nil)
			var visible struct {
				Status, Title         string
				Points, TLDR, Sources []string
			}
			if err := json.Unmarshal(status.Body.Bytes(), &visible); err != nil {
				t.Fatal(err)
			}
			if status.Code != 200 || visible.Status != "ready" || visible.Title != wantTitle || !reflect.DeepEqual(visible.Points, []string{wantPoint}) || !reflect.DeepEqual(visible.TLDR, wantTLDR) || !reflect.DeepEqual(visible.Sources, wantSources) {
				t.Fatalf("status response: %d %s", status.Code, status.Body.String())
			}
			feed := request(http.MethodGet, "/mine", nil)
			if feed.Code != 200 || !strings.Contains(feed.Body.String(), wantTitle) || !strings.Contains(feed.Body.String(), wantPoint) {
				t.Fatalf("summary missing from bookmark feed: %d", feed.Code)
			}
			if task := deliver(); task.Code != http.StatusNoContent {
				t.Fatalf("duplicate task: %d", task.Code)
			}
			mu.Lock()
			gotFetched, gotCalls := append([]string(nil), fetched...), modelCalls
			mu.Unlock()
			if !reflect.DeepEqual(gotFetched, tc.wantFetched) {
				t.Fatalf("fetched=%v want=%v", gotFetched, tc.wantFetched)
			}
			if gotCalls != 2 {
				t.Fatalf("model calls=%d; completed task should not call model again", gotCalls)
			}
		})
	}
}

type imageFlowTransport struct {
	base           *url.URL
	transport      http.RoundTripper
	unexpectedHost func(string)
}

func (tr imageFlowTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "images.example" {
		tr.unexpectedHost(r.URL.Hostname())
		return nil, fmt.Errorf("unexpected outgoing host: %s", r.URL.Hostname())
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme, u.Host = tr.base.Scheme, tr.base.Host
	copy.URL = &u
	response, err := tr.transport.RoundTrip(copy)
	if response != nil {
		response.Request = r
	}
	return response, err
}

func imageFlowFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	result := map[string][]byte{}
	for i := 0; i < 4; i++ {
		img := image.NewRGBA(image.Rect(0, 0, 320, 200))
		img.Set(100, 100, color.RGBA{R: uint8(40 + i*50), G: 100, B: 200, A: 255})
		var data bytes.Buffer
		if err := png.Encode(&data, img); err != nil {
			t.Fatal(err)
		}
		result[fmt.Sprintf("/chart%d.png", i)] = data.Bytes()
	}
	return result
}
func writeImageFlowModelResponse(w http.ResponseWriter, output string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{"finishReason": "STOP", "content": map[string]any{"parts": []any{map[string]string{"text": output}}}}}})
}
