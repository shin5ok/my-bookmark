package iap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

type Identity struct {
	Email   string
	Subject string
}

type Verifier struct {
	Audience string
	KeyURL   string
	Client   *http.Client
	mu       sync.Mutex
	keys     jose.JSONWebKeySet
	updated  time.Time
}

func NewVerifier(audience string) *Verifier {
	return &Verifier{Audience: audience, KeyURL: "https://www.gstatic.com/iap/verify/public_key-jwk", Client: &http.Client{Timeout: 5 * time.Second}}
}

func (v *Verifier) Verify(ctx context.Context, raw string) (Identity, error) {
	if raw == "" || len(raw) > 8192 {
		return Identity{}, errors.New("missing or oversized IAP assertion")
	}
	token, err := jwt.ParseSigned(raw, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil || len(token.Headers) != 1 || token.Headers[0].KeyID == "" {
		return Identity{}, errors.New("invalid IAP assertion header")
	}
	keyID := token.Headers[0].KeyID
	keys, err := v.publicKeys(ctx, keyID)
	if err != nil {
		return Identity{}, err
	}
	var claims struct {
		jwt.Claims
		Email string `json:"email"`
	}
	validSignature := false
	for _, key := range keys.Key(keyID) {
		if (key.Algorithm != "" && key.Algorithm != string(jose.ES256)) || (key.Use != "" && key.Use != "sig") {
			continue
		}
		if err := token.Claims(key.Key, &claims); err == nil {
			validSignature = true
			break
		}
	}
	if !validSignature {
		return Identity{}, errors.New("invalid IAP assertion signature")
	}
	now := time.Now()
	if claims.IssuedAt == nil || claims.Expiry == nil || claims.Subject == "" || claims.Email == "" ||
		claims.Expiry.Time().Sub(claims.IssuedAt.Time()) > 11*time.Minute ||
		claims.ValidateWithLeeway(jwt.Expected{Issuer: "https://cloud.google.com/iap", AnyAudience: jwt.Audience{v.Audience}, Time: now}, 30*time.Second) != nil {
		return Identity{}, errors.New("invalid IAP assertion claims")
	}
	subject := strings.TrimPrefix(claims.Subject, "accounts.google.com:")
	if subject == "" {
		return Identity{}, errors.New("missing IAP subject")
	}
	return Identity{Email: claims.Email, Subject: subject}, nil
}

func (v *Verifier) publicKeys(ctx context.Context, keyID string) (jose.JSONWebKeySet, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if time.Since(v.updated) < time.Hour && len(v.keys.Key(keyID)) > 0 {
		return v.keys, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.KeyURL, nil)
	if err != nil {
		return jose.JSONWebKeySet{}, err
	}
	response, err := v.Client.Do(req)
	if err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch IAP keys: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return jose.JSONWebKeySet{}, fmt.Errorf("fetch IAP keys: HTTP %d", response.StatusCode)
	}
	var keys jose.JSONWebKeySet
	if err := json.NewDecoder(io.LimitReader(response.Body, 256*1024)).Decode(&keys); err != nil {
		return jose.JSONWebKeySet{}, fmt.Errorf("decode IAP keys: %w", err)
	}
	if len(keys.Key(keyID)) == 0 {
		return jose.JSONWebKeySet{}, errors.New("unknown IAP key")
	}
	v.keys, v.updated = keys, time.Now()
	return keys, nil
}
