package web

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
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

// loginTestUserForStats mirrors internal/api's loginTestUser: inserts a
// user + session directly (bypassing the real Google flow) and returns
// a bearer token for it. auth.FromRequest checks the cookie first, then
// falls back to the Authorization header, so a bearer-only request
// (no cookie) is a valid way to authenticate in these tests.
func loginTestUserForStats(t *testing.T, sqlDB *sql.DB, email string) (userID int64, token string) {
	t.Helper()
	ctx := context.Background()
	if err := sqlDB.QueryRowContext(ctx, `
		INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $2) RETURNING id`,
		email, email).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	})
	token, err := auth.RandomToken(32)
	if err != nil {
		t.Fatalf("RandomToken: %v", err)
	}
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		auth.TokenHashForTest(token), userID); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	return userID, token
}

func TestSubmitMoveRedirectsToStatsWhenAuthenticatedGameEnds(t *testing.T) {
	sqlDB, err := db.Open(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("db.Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	authSvc := auth.NewService(sqlDB, "unused", "unused", "unused")
	userID, token := loginTestUserForStats(t, sqlDB, "web-submit-redirect-"+t.Name()+"@example.com")

	// max_mistakes = 1, so the one wrong move below both finishes and
	// fails the game in a single call.
	gameID := "web-submit-redirect-test-game"
	solution := "5" + strings.Repeat("0", 80)
	empty := strings.Repeat("0", 81)
	if _, err := sqlDB.ExecContext(context.Background(), `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status)
		VALUES ($1, $2, $3, $4, $4, $5, 0, 1, 'in_progress')`,
		gameID, userID, int(sudoku.Easy), empty, solution); err != nil {
		t.Fatalf("insert in-progress game fixture: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM games WHERE id = $1", gameID)
	})

	h := NewHandler(game.NewStore(), nil, authSvc, sqlDB, false)
	req := httptest.NewRequest(http.MethodPost, "/play/"+gameID+"/moves",
		strings.NewReader("row=0&col=0&value=9")) // 9 != solution's 5: a mistake
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", gameID)
	rec := httptest.NewRecorder()
	h.submitMove(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/stats" {
		t.Errorf("HX-Redirect = %q, want %q", got, "/stats")
	}
}

func TestSubmitMoveDoesNotRedirectAnonymousGameEnd(t *testing.T) {
	store := game.NewStore()
	var solution sudoku.Grid
	solution[0][0] = 5                                              // != Givens (all zero), so the puzzle isn't trivially already-solved
	p := sudoku.Puzzle{Solution: solution, Difficulty: sudoku.Hard} // MaxMistakes == 3
	g, err := store.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("store.Create() error: %v", err)
	}
	// Drive the game to Failed() with 2 of the 3 allowed mistakes first.
	for i := 0; i < 2; i++ {
		if _, err := store.ApplyMove(context.Background(), g.ID, 0, 0, 9); err != nil {
			t.Fatalf("ApplyMove() setup error: %v", err)
		}
	}

	h := NewHandler(store, nil, nil, nil, false)
	req := httptest.NewRequest(http.MethodPost, "/play/"+g.ID+"/moves",
		strings.NewReader("row=0&col=0&value=9")) // the 3rd (final) mistake
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetPathValue("id", g.ID)
	rec := httptest.NewRecorder()
	h.submitMove(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Redirect"); got != "" {
		t.Errorf("HX-Redirect = %q, want none (anonymous play has no stats page)", got)
	}
	if !strings.Contains(rec.Body.String(), "Game Over") {
		t.Error("response body doesn't contain the board's own Game Over banner")
	}
}

func TestStatsRedirectsAnonymous(t *testing.T) {
	h := NewHandler(game.NewStore(), nil, nil, nil, false)

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rec := httptest.NewRecorder()
	h.stats(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Result().Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want %q", loc, "/")
	}
}

func TestStatsRendersForAuthenticatedUser(t *testing.T) {
	sqlDB, err := db.Open(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("db.Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	authSvc := auth.NewService(sqlDB, "unused", "unused", "unused")

	userID, token := loginTestUserForStats(t, sqlDB, "web-stats-"+t.Name()+"@example.com")

	// One finished game, so the page has something to show beyond zeros.
	gameID := "web-stats-test-game"
	if _, err := sqlDB.ExecContext(context.Background(), `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status)
		VALUES ($1, $2, $3, $4, $4, $4, 2, 5, 'solved')`,
		gameID, userID, int(sudoku.Medium), strings.Repeat("0", 81)); err != nil {
		t.Fatalf("insert finished game fixture: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM games WHERE id = $1", gameID)
	})

	h := NewHandler(game.NewStore(), nil, authSvc, sqlDB, false)
	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.stats(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "medium") {
		t.Error("stats page body doesn't mention the medium difficulty row")
	}
	if !strings.Contains(body, "Solved") {
		t.Error("stats page body doesn't show the solved game in the history list")
	}
}
