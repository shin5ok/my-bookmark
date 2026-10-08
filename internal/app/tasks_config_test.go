package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerConfigRequiresIsolatedService(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	valid := Config{Env: "production", Project: "test-project", BaseURL: "https://worker.run.app", WorkerOnly: true, TasksQueue: "projects/test-project/locations/asia-northeast1/queues/summary", TasksWorkerURL: "https://worker.run.app", TasksServiceAccount: "tasks@test-project.iam.gserviceaccount.com"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.APIOnly = true },
		func(c *Config) { c.WorkerOnly = false },
		func(c *Config) { c.TasksQueue = "" },
		func(c *Config) { c.TasksWorkerURL = "http://worker.run.app" },
		func(c *Config) { c.TasksWorkerURL = "https://worker.run.app/tasks/summary" },
		func(c *Config) { c.TasksServiceAccount = "" },
	} {
		c := valid
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("unsafe task config accepted: %+v", c)
		}
	}
}

func TestPublicAppDoesNotExposeTaskEndpoint(t *testing.T) {
	for _, apiOnly := range []bool{false, true} {
		a, err := New(Config{Env: "development", BaseURL: "http://localhost:8080", APIOnly: apiOnly}, testDB{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/tasks/summary", strings.NewReader(`{}`)))
		if w.Code != 404 {
			t.Fatalf("APIOnly=%v exposed task route: %d", apiOnly, w.Code)
		}
	}
}
