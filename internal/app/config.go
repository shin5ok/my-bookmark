package app

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"my-bookmark/internal/iap"
)

type Config struct {
	APIOnly                                         bool
	WorkerOnly                                      bool
	TasksQueue, TasksWorkerURL, TasksServiceAccount string
	APIBaseURL                                      string
	Project, Port, BaseURL, Env                     string
	GoogleClientID, GoogleClientSecret              string
	GeminiModel, GeminiLocation                     string
	IAPAudience                                     string
	IAPAllowlist                                    iap.Allowlist
}

func LoadConfig() (Config, error) {
	c := Config{Project: os.Getenv("GOOGLE_CLOUD_PROJECT"), Port: env("PORT", "8080"), BaseURL: env("BASE_URL", "http://localhost:8080"), Env: env("APP_ENV", "production"), GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), GeminiModel: env("GEMINI_MODEL", "gemini-3.8-flash"), GeminiLocation: env("GEMINI_LOCATION", "global"), IAPAudience: os.Getenv("IAP_AUDIENCE")}
	if value := os.Getenv("API_ONLY"); value != "" && value != "true" && value != "false" {
		return c, errors.New("API_ONLY must be true or false")
	}
	c.APIOnly = os.Getenv("API_ONLY") == "true"
	if value := os.Getenv("WORKER_ONLY"); value != "" && value != "true" && value != "false" {
		return c, errors.New("WORKER_ONLY must be true or false")
	}
	c.WorkerOnly = os.Getenv("WORKER_ONLY") == "true"
	c.TasksQueue = os.Getenv("TASKS_QUEUE")
	c.TasksWorkerURL = os.Getenv("TASKS_WORKER_URL")
	c.TasksServiceAccount = os.Getenv("TASKS_SERVICE_ACCOUNT")
	c.APIBaseURL = strings.TrimRight(os.Getenv("API_BASE_URL"), "/")
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	if c.IAPAudience != "" || c.APIOnly {
		data, err := os.ReadFile(env("ALLOW_ACCOUNTS_FILE", "allow_accounts.yaml"))
		if err != nil {
			return c, fmt.Errorf("read allow_accounts.yaml: %w", err)
		}
		c.IAPAllowlist, err = iap.ParseAllowlist(data)
		if err != nil {
			return c, err
		}
	}
	return c, c.Validate()
}

var iapAudiencePattern = regexp.MustCompile(`^/projects/[0-9]+/locations/[a-z0-9-]+/services/[a-z0-9-]+$`)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func (c Config) Validate() error {
	if c.WorkerOnly && (c.APIOnly || c.Env != "production") {
		return errors.New("WORKER_ONLY requires a dedicated production service")
	}
	if c.TasksQueue != "" || c.TasksWorkerURL != "" || c.TasksServiceAccount != "" {
		if !regexp.MustCompile(`^projects/[a-z0-9-]+/locations/[a-z0-9-]+/queues/[A-Za-z0-9_-]+$`).MatchString(c.TasksQueue) {
			return errors.New("TASKS_QUEUE must be a full Cloud Tasks queue resource name")
		}
		u, err := url.Parse(c.TasksWorkerURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("TASKS_WORKER_URL must be an HTTPS origin")
		}
		if !regexp.MustCompile(`^[a-z0-9-]+@[a-z0-9-]+\.iam\.gserviceaccount\.com$`).MatchString(c.TasksServiceAccount) {
			return errors.New("TASKS_SERVICE_ACCOUNT must be a service account email")
		}
	}
	if c.WorkerOnly && c.TasksQueue == "" {
		return errors.New("WORKER_ONLY requires Cloud Tasks configuration")
	}
	if c.Env != "production" && c.Env != "development" {
		return errors.New("APP_ENV must be production or development")
	}
	if c.Project == "" {
		return errors.New("GOOGLE_CLOUD_PROJECT is required")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("BASE_URL must be an origin without a path")
	}
	if c.Env == "production" {
		if u.Scheme != "https" {
			return errors.New("production BASE_URL must use https")
		}
		if os.Getenv("FIRESTORE_EMULATOR_HOST") != "" {
			return errors.New("production cannot use Firestore Emulator")
		}
		if !c.WorkerOnly && ((!c.APIOnly && c.IAPAudience == "") || len(c.IAPAllowlist.Members()) == 0) {
			return errors.New("IAP_AUDIENCE and allow_accounts.yaml are required in production")
		}
	} else {
		if os.Getenv("K_SERVICE") != "" {
			return errors.New("development mode is forbidden on Cloud Run")
		}
		if u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1") {
			return errors.New("development BASE_URL must be http on localhost")
		}
		if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
			return errors.New("development requires FIRESTORE_EMULATOR_HOST")
		}
	}
	if c.APIBaseURL != "" {
		u, err := url.Parse(c.APIBaseURL)
		if err != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(c.Env == "development" && u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
			return errors.New("API_BASE_URL must be an HTTPS origin (localhost HTTP is allowed in development)")
		}
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		return errors.New("set both Google OAuth credentials")
	}
	if c.IAPAudience != "" && !iapAudiencePattern.MatchString(c.IAPAudience) {
		return errors.New("IAP_AUDIENCE must identify this Cloud Run service")
	}
	return nil
}
func (c Config) secure() bool { return strings.HasPrefix(c.BaseURL, "https://") }
func (c Config) cookieName() string {
	if c.secure() {
		return "__Host-shiori_session"
	}
	return "shiori_session"
}
func (c Config) oauthCookie() string {
	if c.secure() {
		return "__Host-shiori_oauth"
	}
	return "shiori_oauth"
}
