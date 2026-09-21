package app

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"golang.org/x/oauth2"
	"my-bookmark/internal/store"
)

type authDB struct {
	Database
	state    string
	value    store.OAuthState
	consumed bool
	created  *store.Session
	deleted  string
}

func (d *authDB) PutOAuth(_ context.Context, s string, v store.OAuthState) error {
	d.state = s
	d.value = v
	return nil
}
func (d *authDB) ConsumeOAuth(_ context.Context, s string) (store.OAuthState, error) {
	if s != d.state || d.consumed {
		return store.OAuthState{}, store.ErrNotFound
	}
	d.consumed = true
	return d.value, nil
}
func (d *authDB) PutSession(_ context.Context, _ string, s store.Session) error {
	d.created = &s
	return nil
}
func (d *authDB) DeleteSession(_ context.Context, s string) error { d.deleted = s; return nil }
func (d *authDB) Session(context.Context, string) (store.Session, error) {
	return store.Session{}, store.ErrNotFound
}
func TestGoogleLoginCallback(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name               string
		modify             func(map[string]any)
		wrongKey, badState bool
		want               int
	}{
		{name: "valid", want: 303},
		{name: "legacy Google issuer", modify: func(c map[string]any) { c["iss"] = "accounts.google.com" }, want: 303},
		{name: "wrong nonce", modify: func(c map[string]any) { c["nonce"] = "other" }, want: 400},
		{name: "wrong audience", modify: func(c map[string]any) { c["aud"] = "other" }, want: 400},
		{name: "wrong issuer", modify: func(c map[string]any) { c["iss"] = "https://evil.example" }, want: 400},
		{name: "expired", modify: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, want: 400},
		{name: "bad signature", wrongKey: true, want: 400},
		{name: "mismatched state", badState: true, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &authDB{}
			tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
				}
				if r.FormValue("code_verifier") != db.value.Verifier {
					t.Error("PKCE verifier missing")
				}
				claims := map[string]any{"iss": "https://accounts.google.com", "aud": "client-id", "sub": "google-subject", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": db.value.Nonce, "name": "読者"}
				if tc.modify != nil {
					tc.modify(claims)
				}
				signKey := key
				if tc.wrongKey {
					signKey = wrong
				}
				signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: signKey}, nil)
				if e != nil {
					t.Error(e)
					http.Error(w, "error", 500)
					return
				}
				raw, e := jwt.Signed(signer).Claims(claims).Serialize()
				if e != nil {
					t.Error(e)
					http.Error(w, "error", 500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"access_token": "test-only-token", "token_type": "Bearer", "id_token": raw, "expires_in": 3600})
			}))
			defer tokenServer.Close()
			a, err := New(Config{Env: "production", BaseURL: "https://bookmark.example"}, db, nil)
			if err != nil {
				t.Fatal(err)
			}
			a.oauth = &oauth2.Config{ClientID: "client-id", ClientSecret: "test-secret", RedirectURL: "https://bookmark.example/auth/google/callback", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: tokenServer.URL}, Scopes: []string{"openid", "profile"}}
			a.verifier = oidc.NewVerifier("https://accounts.google.com", &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: "client-id"})
			login := httptest.NewRecorder()
			a.Handler().ServeHTTP(login, httptest.NewRequest("GET", "/auth/google", nil))
			redirect, err := url.Parse(login.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			q := redirect.Query()
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") != db.value.Nonce {
				t.Fatal("missing PKCE or nonce")
			}
			state := q.Get("state")
			if tc.badState {
				state = "wrong"
			}
			request := httptest.NewRequest("GET", "/auth/google/callback?code=test-code&state="+url.QueryEscape(state), nil)
			for _, c := range login.Result().Cookies() {
				request.AddCookie(c)
			}
			response := httptest.NewRecorder()
			a.Handler().ServeHTTP(response, request)
			if response.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == 303 {
				if db.created == nil || db.created.CSRF == "" || db.created.User.ID != store.Hash("https://accounts.google.com|google-subject") {
					t.Fatal("session missing")
				}
				found := false
				for _, c := range response.Result().Cookies() {
					if c.Name == "__Host-shiori_session" {
						found = true
						if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
							t.Fatalf("unsafe cookie %#v", c)
						}
					}
				}
				if !found {
					t.Fatal("no session cookie")
				}
				replay := httptest.NewRecorder()
				a.Handler().ServeHTTP(replay, request.Clone(context.Background()))
				if replay.Code != 400 {
					t.Fatal("OAuth state replay accepted")
				}
			} else if db.created != nil {
				t.Fatal("invalid identity created session")
			}
		})
	}
}
