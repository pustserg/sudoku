package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
)

// testDatabaseURL mirrors internal/api's and internal/db's helper:
// tests that need a real, already-migrated Postgres database skip
// without one.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SUDOKU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SUDOKU_TEST_DATABASE_URL not set, skipping integration test")
	}
	return url
}

// TestAuthFallbackToAnon mirrors internal/api's test of the same
// function name: it exercises storeFor's error-classification logic in
// isolation, since triggering a genuine non-ErrNoSession failure out of
// a real auth.Service.Authenticate would need fault injection into the
// database connection. Only auth.ErrNoSession should mean "fall back to
// anonymous"; anything else must be propagated as a real error rather
// than silently downgrading a logged-in user to an unpersisted
// anonymous game.
func TestAuthFallbackToAnon(t *testing.T) {
	if !authFallbackToAnon(auth.ErrNoSession) {
		t.Error("authFallbackToAnon(auth.ErrNoSession) = false, want true")
	}
	if !authFallbackToAnon(fmt.Errorf("lookup session: %w", auth.ErrNoSession)) {
		t.Error("authFallbackToAnon(wrapped auth.ErrNoSession) = false, want true (errors.Is should still match)")
	}

	other := errors.New("boom: database connection lost")
	if authFallbackToAnon(other) {
		t.Error("authFallbackToAnon(other error) = true, want false: a genuine failure must not be silently treated as no-session")
	}
}

// TestNewStateCookieAttributes and TestNewSessionCookieAttributes pin
// the fix for the missing Secure/SameSite cookie attributes: SameSite
// must always be Lax (Strict would break the OAuth redirect flow, which
// needs the state cookie to survive a top-level cross-site GET redirect
// from Google), and Secure must track h.secureCookies rather than being
// hardcoded, since a hardcoded true would break login over plain HTTP
// in local dev.
func TestNewStateCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		h := &Handler{secureCookies: secure}
		c := h.newStateCookie("the-state", 600)

		if c.Name != stateCookieName {
			t.Errorf("secureCookies=%v: Name = %q, want %q", secure, c.Name, stateCookieName)
		}
		if c.Value != "the-state" {
			t.Errorf("secureCookies=%v: Value = %q, want %q", secure, c.Value, "the-state")
		}
		if !c.HttpOnly {
			t.Errorf("secureCookies=%v: HttpOnly = false, want true", secure)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("secureCookies=%v: SameSite = %v, want SameSiteLaxMode", secure, c.SameSite)
		}
		if c.Secure != secure {
			t.Errorf("secureCookies=%v: Secure = %v, want %v", secure, c.Secure, secure)
		}
		if c.MaxAge != 600 {
			t.Errorf("secureCookies=%v: MaxAge = %d, want 600", secure, c.MaxAge)
		}
	}
}

func TestNewSessionCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		h := &Handler{secureCookies: secure}
		c := h.newSessionCookie("the-token", sessionCookieMaxAge)

		if c.Name != auth.CookieName {
			t.Errorf("secureCookies=%v: Name = %q, want %q", secure, c.Name, auth.CookieName)
		}
		if c.Value != "the-token" {
			t.Errorf("secureCookies=%v: Value = %q, want %q", secure, c.Value, "the-token")
		}
		if !c.HttpOnly {
			t.Errorf("secureCookies=%v: HttpOnly = false, want true", secure)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("secureCookies=%v: SameSite = %v, want SameSiteLaxMode", secure, c.SameSite)
		}
		if c.Secure != secure {
			t.Errorf("secureCookies=%v: Secure = %v, want %v", secure, c.Secure, secure)
		}
	}
}

// TestGoogleLoginSetsStateCookieWithConfiguredSecureness drives the
// actual handler (not just the helper) to confirm the Set-Cookie header
// googleLogin emits carries the right attributes end to end.
// googleLogin doesn't touch the database, so this needs no
// SUDOKU_TEST_DATABASE_URL.
func TestGoogleLoginSetsStateCookieWithConfiguredSecureness(t *testing.T) {
	for _, secure := range []bool{true, false} {
		authSvc := auth.NewService(nil, "client-id", "client-secret", "https://example.com/auth/google/callback")
		h := NewHandler(game.NewStore(), nil, authSvc, nil, secure)

		req := httptest.NewRequest(http.MethodGet, "/auth/google/login", nil)
		rec := httptest.NewRecorder()
		h.googleLogin(rec, req)

		resp := rec.Result()
		var found *http.Cookie
		for _, c := range resp.Cookies() {
			if c.Name == stateCookieName {
				found = c
			}
		}
		if found == nil {
			t.Fatalf("secureCookies=%v: no %q cookie set by googleLogin", secure, stateCookieName)
		}
		if found.Secure != secure {
			t.Errorf("secureCookies=%v: state cookie Secure = %v, want %v", secure, found.Secure, secure)
		}
		if found.SameSite != http.SameSiteLaxMode {
			t.Errorf("secureCookies=%v: state cookie SameSite = %v, want SameSiteLaxMode", secure, found.SameSite)
		}
	}
}

// TestLogoutClearsSessionCookieWithConfiguredSecureness drives the real
// logout handler (backed by a real auth.Service/database, mirroring
// internal/api's TestLogoutClearsSession) to confirm the clearing
// cookie it emits also carries the right attributes.
func TestLogoutClearsSessionCookieWithConfiguredSecureness(t *testing.T) {
	sqlDB, err := db.Open(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("db.Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	authSvc := auth.NewService(sqlDB, "unused", "unused", "unused")

	var userID int64
	if err := sqlDB.QueryRowContext(context.Background(), `
		INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $2) RETURNING id`,
		game.NewID(), "web-logout-test@example.com").Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", userID)
	})
	token, err := auth.RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(), `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		auth.TokenHashForTest(token), userID); err != nil {
		t.Fatalf("insert test session: %v", err)
	}

	h := NewHandler(game.NewStore(), nil, authSvc, sqlDB, true)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: token})
	rec := httptest.NewRecorder()
	h.logout(rec, req)

	resp := rec.Result()
	var found *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.CookieName {
			found = c
		}
	}
	if found == nil {
		t.Fatalf("no %q cookie set by logout", auth.CookieName)
	}
	if found.Value != "" {
		t.Errorf("cleared session cookie Value = %q, want empty", found.Value)
	}
	if found.MaxAge >= 0 {
		t.Errorf("cleared session cookie MaxAge = %d, want negative (delete)", found.MaxAge)
	}
	if !found.Secure {
		t.Error("cleared session cookie Secure = false, want true (secureCookies=true)")
	}
	if found.SameSite != http.SameSiteLaxMode {
		t.Errorf("cleared session cookie SameSite = %v, want SameSiteLaxMode", found.SameSite)
	}
}
