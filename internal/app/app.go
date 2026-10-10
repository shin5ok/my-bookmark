package app

import (
	"context"
	"crypto/subtle"
	"errors"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/oauth2"
	"my-bookmark/internal/content"
	"my-bookmark/internal/iap"
	"my-bookmark/internal/store"
	"my-bookmark/internal/summary"
	"my-bookmark/internal/web"
)

type Database interface {
	PutAPIToken(context.Context, store.APIToken) error
	APIToken(context.Context, string) (store.APIToken, error)
	DeleteAPIToken(context.Context, string) error
	List(context.Context, string, string, string) ([]store.Entry, string, error)
	Get(context.Context, string) (store.Article, error)
	Own(context.Context, string, string) (*store.Bookmark, error)
	Save(context.Context, store.User, string, string, []string) (store.Article, error)
	Delete(context.Context, string, string) error
	SetUnderstood(context.Context, string, string, bool) error
	SetRating(context.Context, string, string, int) (int64, error)
	Comments(context.Context, string) ([]store.Bookmark, error)
	Claim(context.Context, string, string) (string, error)
	Finish(context.Context, string, string, string, []string, string, bool) error
	Enqueue(context.Context, string, string, summary.Style, string) (string, error)
	Session(context.Context, string) (store.Session, error)
	PutSession(context.Context, string, store.Session) error
	DeleteSession(context.Context, string) error
	PutOAuth(context.Context, string, store.OAuthState) error
	ConsumeOAuth(context.Context, string) (store.OAuthState, error)
}
type Summarizer interface {
	Summarize(context.Context, string, string, summary.Style, string) (summary.Result, error)
}
type App struct {
	cfg         Config
	db          Database
	summary     Summarizer
	fetcher     *http.Client
	templates   *template.Template
	oauth       *oauth2.Config
	verifier    *oidc.IDTokenVerifier
	iapVerifier interface {
		Verify(context.Context, string) (iap.Identity, error)
	}
}

func New(cfg Config, db Database, summary Summarizer) (*App, error) {
	funcs := template.FuncMap{"stars": func() []int { return []int{1, 2, 3, 4, 5} }, "join": strings.Join, "date": func(t time.Time) string { return t.In(time.FixedZone("JST", 9*3600)).Format("2006.01.02") }, "host": func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return raw
		}
		return u.Hostname()
	}, "summaryState": summaryData}
	tmpl, err := template.New("pages").Funcs(funcs).ParseFS(web.Files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, db: db, summary: summary, fetcher: content.NewFetcher(), templates: tmpl}
	if cfg.APIOnly {
		return a, nil
	}
	if cfg.IAPAudience != "" {
		a.iapVerifier = iap.NewVerifier(cfg.IAPAudience)
	} else if cfg.GoogleClientID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		provider, err := oidc.NewProvider(ctx, "https://accounts.google.com")
		if err != nil {
			return nil, err
		}
		a.oauth = &oauth2.Config{ClientID: cfg.GoogleClientID, ClientSecret: cfg.GoogleClientSecret, RedirectURL: cfg.BaseURL + "/auth/google/callback", Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "profile"}}
		a.verifier = provider.Verifier(&oidc.Config{ClientID: cfg.GoogleClientID})
	}
	return a, nil
}

type sessionKey struct{}

func session(r *http.Request) *store.Session {
	s, _ := r.Context().Value(sessionKey{}).(*store.Session)
	return s
}
func (a *App) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, a.security)
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	if a.cfg.APIOnly || a.cfg.Env == "development" {
		r.Post("/api/bookmarks", a.saveAPI)
		r.Put("/api/bookmarks", a.saveAPI)
	}
	if a.cfg.APIOnly {
		return r
	}
	static, _ := fs.Sub(web.Files, "static")
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	r.Group(func(r chi.Router) {
		r.Use(a.withSession)
		r.Get("/", a.feed)
		r.Get("/new", a.feed)
		r.Get("/popular", a.feed)
		r.Get("/mine", a.feed)
		r.With(a.requireSession).Get("/settings/api", a.tokenSettings)
		r.Get("/articles/{id}", a.detail)
		r.Get("/articles/{id}/summary", a.summaryStatus)
		r.Get("/auth/google", a.login)
		r.Get("/auth/google/callback", a.callback)
		r.Group(func(r chi.Router) {
			r.Use(a.requireSession, a.csrf)
			r.Post("/settings/api/issue", a.issueToken)
			r.Post("/settings/api/revoke", a.revokeToken)
			r.Post("/bookmarks", a.save)
			r.Post("/bookmarks/{id}/delete", a.remove)
			r.Post("/bookmarks/{id}/understood", a.setUnderstood)
			r.Post("/bookmarks/{id}/rating", a.setRating)
			r.Post("/articles/{id}/summary", a.generate)
			r.Post("/logout", a.logout)
		})
	})
	return r
}
func (a *App) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if a.cfg.secure() {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		if !strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
		next.ServeHTTP(w, r)
	})
}
func (a *App) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.iapVerifier != nil {
			identity, err := a.iapVerifier.Verify(r.Context(), r.Header.Get("X-Goog-IAP-JWT-Assertion"))
			if err != nil {
				http.Error(w, "IAP認証を確認できませんでした。", http.StatusUnauthorized)
				return
			}
			if !a.cfg.IAPAllowlist.Allows(identity.Email) {
				http.Error(w, "このアカウントにはアクセス権がありません。", http.StatusForbidden)
				return
			}
			uid := store.Hash("https://accounts.google.com|" + identity.Subject)
			if c, err := r.Cookie(a.cfg.cookieName()); err == nil && len(c.Value) <= 128 {
				s, err := a.db.Session(r.Context(), c.Value)
				if err == nil && s.User.ID == uid {
					s.Email = identity.Email
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, &s)))
					return
				}
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					a.fail(w, r, err)
					return
				}
			}
			key := store.RandomToken()
			name, _, _ := strings.Cut(identity.Email, "@")
			if len(name) > 80 {
				name = name[:80]
			}
			s := store.Session{Email: identity.Email, User: store.User{ID: uid, Name: name}, CSRF: store.RandomToken(), ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
			if err := a.db.PutSession(r.Context(), key, s); err != nil {
				a.fail(w, r, err)
				return
			}
			a.cookie(w, a.cfg.cookieName(), key, 7*24*3600)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, &s)))
			return
		}
		if c, err := r.Cookie(a.cfg.cookieName()); err == nil && len(c.Value) <= 128 {
			s, err := a.db.Session(r.Context(), c.Value)
			if err == nil {
				r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, &s))
			} else if !errors.Is(err, store.ErrNotFound) {
				a.fail(w, r, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if session(r) == nil {
			a.problem(w, r, 401, "Googleにログインしてから操作してください。")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			a.problem(w, r, 400, "入力内容が大きすぎるか、形式が正しくありません。")
			return
		}
		origin := r.Header.Get("Origin")
		s := session(r)
		if (origin != "" && origin != a.cfg.BaseURL) || s == nil || s.CSRF == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(s.CSRF)) != 1 {
			a.problem(w, r, 403, "ページを再読み込みして、もう一度操作してください。")
			return
		}
		next.ServeHTTP(w, r)
	})
}

var validID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (a *App) id(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !validID.MatchString(id) {
		a.problem(w, r, 404, "記事が見つかりません。")
		return "", false
	}
	return id, true
}
func (a *App) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		a.problem(w, r, 404, "記事が見つかりません。")
		return
	}
	slog.Error("request failed", "request_id", middleware.GetReqID(r.Context()), "error", err)
	a.problem(w, r, 503, "データを読み込めませんでした。少し待ってから再試行してください。")
}
