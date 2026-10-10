package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"my-bookmark/internal/summary"
)

var ErrNotFound = errors.New("not found")
var ErrBusy = errors.New("summary already processing or available")
var ErrRateLimited = errors.New("summary quota exceeded")

type User struct {
	ID   string `firestore:"id"`
	Name string `firestore:"name"`
}
type Article struct {
	ID          string    `firestore:"-"`
	URL         string    `firestore:"url"`
	Title       string    `firestore:"title"`
	Domain      string    `firestore:"domain"`
	TLDR        []string  `firestore:"tldr"`
	Points      []string  `firestore:"points"`
	Status      string    `firestore:"status"`
	Model       string    `firestore:"model"`
	RatingTotal int64     `firestore:"rating_total"`
	Count       int64     `firestore:"count"`
	Active      bool      `firestore:"active"`
	CreatedAt   time.Time `firestore:"created_at"`
	LeaseUntil  time.Time `firestore:"lease_until"`
	Lease       string    `firestore:"lease"`
	Stage       string    `firestore:"stage"`
	Progress    string    `firestore:"progress"`
	LastError   string    `firestore:"last_error"`
	Sources     []string  `firestore:"sources"`
}
type SummaryJob struct {
	Instruction string        `firestore:"instruction"`
	Style       summary.Style `firestore:"style"`
	ArticleID   string        `firestore:"article_id"`
	UserID      string        `firestore:"user_id"`
	Status      string        `firestore:"status"`
	Stage       string        `firestore:"stage"`
	QueuedAt    time.Time     `firestore:"queued_at"`
	Lease       string        `firestore:"lease"`
	LeaseUntil  time.Time     `firestore:"lease_until"`
	Attempts    int           `firestore:"attempts"`
	LastError   string        `firestore:"last_error"`
}
type Bookmark struct {
	ArticleID  string    `firestore:"article_id"`
	UserID     string    `firestore:"user_id"`
	Name       string    `firestore:"name"`
	Comment    string    `firestore:"comment"`
	Tags       []string  `firestore:"tags"`
	Rating     int       `firestore:"rating"`
	Understood bool      `firestore:"understood"`
	CreatedAt  time.Time `firestore:"created_at"`
	UpdatedAt  time.Time `firestore:"updated_at"`
}
type Entry struct {
	Article  Article
	Bookmark *Bookmark
}
type APIToken struct {
	User      User      `firestore:"user"`
	Email     string    `firestore:"email"`
	Hash      string    `firestore:"hash"`
	ExpiresAt time.Time `firestore:"expires_at"`
}

type Session struct {
	Email     string    `firestore:"email"`
	User      User      `firestore:"user"`
	CSRF      string    `firestore:"csrf"`
	ExpiresAt time.Time `firestore:"expires_at"`
}
type OAuthState struct {
	Nonce     string    `firestore:"nonce"`
	Verifier  string    `firestore:"verifier"`
	ExpiresAt time.Time `firestore:"expires_at"`
}

func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func RandomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
