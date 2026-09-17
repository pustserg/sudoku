// Package auth contains OAuth login and session handling: exchanging a
// Google authorization code for a signed-in user, and issuing/verifying
// the opaque session tokens used by both the web UI (cookie) and the
// API (bearer token).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// CookieName is the name of the httpOnly cookie the web UI stores a
// session token in.
const CookieName = "session"

// googleUserInfoURL is Google's OpenID Connect userinfo endpoint,
// returning at least "sub" (stable per-account ID) and "email" as JSON.
const googleUserInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// sessionLifetime is how long a session token stays valid after login.
const sessionLifetime = 30 * 24 * time.Hour

// ErrNoSession is returned by Authenticate when token is empty, unknown,
// or expired. Callers treat it as "proceed anonymously," not as a fatal
// error.
var ErrNoSession = errors.New("auth: no valid session")

// User is a signed-in user, as resolved from a session token.
type User struct {
	ID    int64
	Email string
}

// Service handles the Google OAuth flow and session issuance/lookup
// against the users/sessions tables.
type Service struct {
	db          *sql.DB
	oauth       *oauth2.Config
	userInfoURL string
}

// NewService returns a Service that logs users in via Google OAuth
// using the given client credentials and redirect URL, and stores
// users/sessions in sqlDB.
func NewService(sqlDB *sql.DB, clientID, clientSecret, redirectURL string) *Service {
	return &Service{
		db: sqlDB,
		oauth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email"},
			Endpoint:     google.Endpoint,
		},
		userInfoURL: googleUserInfoURL,
	}
}

// LoginURL returns the Google consent-screen URL to redirect the
// browser to, embedding state (an anti-CSRF token the caller must
// generate, store, and check against the callback's state parameter).
func (s *Service) LoginURL(state string) string {
	return s.oauth.AuthCodeURL(state)
}

// HandleCallback exchanges an OAuth authorization code for the signed-in
// user's profile, upserts a users row for them, issues a new session,
// and returns the raw (unhashed) session token to give to the caller —
// it is never stored or logged in this form.
func (s *Service) HandleCallback(ctx context.Context, code string) (string, error) {
	tok, err := s.oauth.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("auth: exchange code: %w", err)
	}

	client := s.oauth.Client(ctx, tok)
	resp, err := client.Get(s.userInfoURL)
	if err != nil {
		return "", fmt.Errorf("auth: fetch userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth: userinfo request failed: status %d", resp.StatusCode)
	}
	var info struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", fmt.Errorf("auth: decode userinfo: %w", err)
	}

	var userID int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO users (provider, provider_user_id, email)
		VALUES ('google', $1, $2)
		ON CONFLICT (provider, provider_user_id) DO UPDATE SET email = EXCLUDED.email
		RETURNING id`, info.Sub, info.Email).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("auth: upsert user: %w", err)
	}

	token, err := RandomToken(32)
	if err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash(token), userID, time.Now().Add(sessionLifetime)); err != nil {
		return "", fmt.Errorf("auth: create session: %w", err)
	}
	return token, nil
}

// Authenticate resolves token to the signed-in User it belongs to, or
// ErrNoSession if token is empty, unknown, or its session has expired.
func (s *Service) Authenticate(ctx context.Context, token string) (*User, error) {
	if token == "" {
		return nil, ErrNoSession
	}
	var u User
	var expiresAt time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT users.id, users.email, sessions.expires_at
		FROM sessions JOIN users ON users.id = sessions.user_id
		WHERE sessions.token_hash = $1`, tokenHash(token)).Scan(&u.ID, &u.Email, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("auth: lookup session: %w", err)
	}
	if time.Now().After(expiresAt) {
		return nil, ErrNoSession
	}
	return &u, nil
}

// Logout deletes the session token belongs to, if any. A missing or
// already-invalid token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = $1", tokenHash(token))
	if err != nil {
		return fmt.Errorf("auth: delete session: %w", err)
	}
	return nil
}

// RandomToken returns a random hex-encoded token of n random bytes,
// suitable for session tokens or OAuth state values.
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate random token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// tokenHash returns the SHA-256 hash of token, as stored in
// sessions.token_hash — the raw token itself is never persisted.
func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// FromRequest returns the session token carried by r: the CookieName
// cookie (web UI) if present, otherwise the "Authorization: Bearer
// <token>" header (API clients), otherwise "".
func FromRequest(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}
