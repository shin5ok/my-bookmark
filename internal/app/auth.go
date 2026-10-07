package app

import (
	"context"
	"crypto/subtle"
	"net/http"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"my-bookmark/internal/store"
)

func (a *App) cookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.cfg.secure(), SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if a.iapVerifier != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if a.oauth == nil {
		a.problem(w, r, 503, "Googleログインは準備中です。管理者が認証設定を行うと利用できます。")
		return
	}
	state := store.RandomToken()
	nonce := store.RandomToken()
	verifier := oauth2.GenerateVerifier()
	if err := a.db.PutOAuth(r.Context(), state, store.OAuthState{Nonce: nonce, Verifier: verifier, ExpiresAt: time.Now().Add(10 * time.Minute)}); err != nil {
		a.fail(w, r, err)
		return
	}
	a.cookie(w, a.cfg.oauthCookie(), state, 600)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), http.StatusFound)
}
func (a *App) callback(w http.ResponseWriter, r *http.Request) {
	a.cookie(w, a.cfg.oauthCookie(), "", -1)
	state := r.URL.Query().Get("state")
	cookie, err := r.Cookie(a.cfg.oauthCookie())
	if a.oauth == nil || err != nil || state == "" || len(state) > 128 || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		a.problem(w, r, 400, "ログインの確認に失敗しました。もう一度ログインしてください。")
		return
	}
	value, err := a.db.ConsumeOAuth(r.Context(), state)
	if err != nil {
		a.problem(w, r, 400, "ログインの有効期限が切れました。もう一度ログインしてください。")
		return
	}
	if r.URL.Query().Get("error") != "" || r.URL.Query().Get("code") == "" {
		a.problem(w, r, 400, "Googleログインがキャンセルされました。")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := a.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(value.Verifier))
	if err != nil {
		a.problem(w, r, 400, "Google認証に失敗しました。もう一度ログインしてください。")
		return
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		a.problem(w, r, 400, "Google認証情報を確認できませんでした。")
		return
	}
	identity, err := a.verifier.Verify(ctx, raw)
	if err != nil || identity.Subject == "" || subtle.ConstantTimeCompare([]byte(identity.Nonce), []byte(value.Nonce)) != 1 {
		a.problem(w, r, 400, "Google認証情報を確認できませんでした。")
		return
	}
	var claims struct {
		Name string `json:"name"`
	}
	if err = identity.Claims(&claims); err != nil {
		a.problem(w, r, 400, "Googleプロフィールを確認できませんでした。")
		return
	}
	if claims.Name == "" {
		claims.Name = "読者"
	}
	if len([]rune(claims.Name)) > 80 {
		claims.Name = string([]rune(claims.Name)[:80])
	}
	// Google accepts both issuer spellings; use one namespace after verification.
	user := store.User{ID: store.Hash("https://accounts.google.com|" + identity.Subject), Name: claims.Name}
	key := store.RandomToken()
	s := store.Session{User: user, CSRF: store.RandomToken(), ExpiresAt: time.Now().Add(7 * 24 * time.Hour)}
	if err = a.db.PutSession(ctx, key, s); err != nil {
		a.fail(w, r, err)
		return
	}
	if old, err := r.Cookie(a.cfg.cookieName()); err == nil {
		if err = a.db.DeleteSession(ctx, old.Value); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	a.cookie(w, a.cfg.cookieName(), key, 7*24*3600)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(a.cfg.cookieName()); err == nil {
		if err = a.db.DeleteSession(r.Context(), c.Value); err != nil {
			a.fail(w, r, err)
			return
		}
	}
	a.cookie(w, a.cfg.cookieName(), "", -1)
	if a.iapVerifier != nil {
		http.Redirect(w, r, "/?gcp-iap-mode=CLEAR_LOGIN_COOKIE", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
