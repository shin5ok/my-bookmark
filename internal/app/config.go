package app

import (
	"errors"
	"net/url"
	"os"
	"strings"
)

type Config struct{ Project, Port, BaseURL, Env, GoogleClientID, GoogleClientSecret, GeminiKey, GeminiModel string }

func LoadConfig() (Config, error) {
	c := Config{Project: os.Getenv("GOOGLE_CLOUD_PROJECT"), Port: env("PORT", "8080"), BaseURL: env("BASE_URL", "http://localhost:8080"), Env: env("APP_ENV", "production"), GoogleClientID: os.Getenv("GOOGLE_CLIENT_ID"), GoogleClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"), GeminiKey: os.Getenv("GEMINI_API_KEY"), GeminiModel: env("GEMINI_MODEL", "gemini-3.8-flash")}
	c.BaseURL = strings.TrimRight(c.BaseURL, "/")
	return c, c.Validate()
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func (c Config) Validate() error {
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
		if c.GoogleClientID == "" || c.GoogleClientSecret == "" || c.GeminiKey == "" {
			return errors.New("Google OAuth credentials and GEMINI_API_KEY are required in production")
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
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		return errors.New("set both Google OAuth credentials")
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
