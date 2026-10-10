package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"my-bookmark/internal/app"
)

func TestCheckChromiumOnlyForRenderingProcesses(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  app.Config
		want bool
	}{
		{"worker", app.Config{WorkerOnly: true, TasksQueue: "queue"}, true},
		{"local worker", app.Config{Env: "development"}, true},
		{"legacy worker", app.Config{Env: "production"}, true},
		{"API", app.Config{APIOnly: true}, false},
		{"dispatcher", app.Config{TasksQueue: "queue"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			ctx := context.Background()
			err := checkChromium(ctx, tc.cfg, func(got context.Context) error {
				called = true
				if got != ctx {
					t.Fatal("startup context was not passed to probe")
				}
				return nil
			})
			if err != nil || called != tc.want {
				t.Fatalf("called=%v err=%v", called, err)
			}
		})
	}
}

func TestCheckChromiumPreservesStartupFailure(t *testing.T) {
	want := errors.New("sandbox unavailable")
	err := checkChromium(context.Background(), app.Config{WorkerOnly: true}, func(context.Context) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("startup failure lost: %v", err)
	}
}

func TestServerStopsWhenChromiumCannotStart(t *testing.T) {
	for name, value := range map[string]string{
		"APP_ENV": "development", "BASE_URL": "http://localhost:8080",
		"GOOGLE_CLOUD_PROJECT": "demo-bookmark", "FIRESTORE_EMULATOR_HOST": "127.0.0.1:8085",
		"API_ONLY": "false", "WORKER_ONLY": "false", "K_SERVICE": "",
		"TASKS_QUEUE": "", "TASKS_WORKER_URL": "", "TASKS_SERVICE_ACCOUNT": "",
		"IAP_AUDIENCE": "", "GOOGLE_CLIENT_ID": "", "GOOGLE_CLIENT_SECRET": "", "API_BASE_URL": "",
		"CHROME_BIN": filepath.Join(t.TempDir(), "missing-chrome"),
	} {
		t.Setenv(name, value)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "Chromium sandbox startup check failed") {
		t.Fatalf("server must stop before database access or listening: %v", err)
	}
}
