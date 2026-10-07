package iap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestVerifierRejectsInvalidIAPAssertions(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keys := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test", Algorithm: string(jose.ES256), Use: "sig"}}}
	verifier := NewVerifier("/projects/123/locations/asia-northeast1/services/shiori")
	verifier.Client = &http.Client{Transport: keyTransport{keys}}
	now := time.Now()
	for _, tc := range []struct {
		name     string
		modify   func(map[string]any)
		wrongKey bool
		want     bool
	}{
		{"valid", nil, false, true},
		{"wrong audience", func(c map[string]any) { c["aud"] = "other" }, false, false},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://evil.example" }, false, false},
		{"expired", func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() }, false, false},
		{"too long", func(c map[string]any) { c["iat"] = now.Add(-time.Hour).Unix() }, false, false},
		{"missing email", func(c map[string]any) { delete(c, "email") }, false, false},
		{"wrong signature", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"iss": "https://cloud.google.com/iap", "aud": verifier.Audience, "sub": "accounts.google.com:subject", "email": "reader@data-cloud.jp", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()}
			if tc.modify != nil {
				tc.modify(claims)
			}
			signKey := key
			if tc.wrongKey {
				signKey = other
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: signKey, KeyID: "test"}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := jwt.Signed(signer).Claims(claims).Serialize()
			if err != nil {
				t.Fatal(err)
			}
			identity, err := verifier.Verify(context.Background(), raw)
			if (err == nil) != tc.want {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
			if tc.want && (identity.Email != "reader@data-cloud.jp" || identity.Subject != "subject") {
				t.Fatalf("identity=%+v", identity)
			}
		})
	}
}

type keyTransport struct{ keys jose.JSONWebKeySet }

func (k keyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	json.NewEncoder(w).Encode(k.keys)
	return w.Result(), nil
}
