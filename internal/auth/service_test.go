package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/pustserg/sudoku/internal/db"
)

// fakeGoogle runs an httptest.Server that stands in for Google's token
// and userinfo endpoints, so tests never make real network calls. sub
// and email are baked into every successful token exchange.
func fakeGoogle(t *testing.T, sub, email string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sub":   sub,
			"email": email,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// testDatabaseURL mirrors internal/db's helper: these tests need a real,
// already-migrated Postgres database and skip without one.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SUDOKU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SUDOKU_TEST_DATABASE_URL not set, skipping integration test")
	}
	return url
}

// newTestService returns a Service wired to google, whose token and
// userinfo endpoints point at a fakeGoogle server instead of the real
// Google, plus the *sql.DB it used (for cleanup).
func newTestService(t *testing.T, google *httptest.Server) *Service {
	t.Helper()
	sqlDB, err := db.Open(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("db.Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	svc := &Service{
		db: sqlDB,
		oauth: &oauth2.Config{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
			RedirectURL:  "http://localhost/auth/google/callback",
			Scopes:       []string{"openid", "email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  google.URL + "/auth",
				TokenURL: google.URL + "/token",
			},
		},
		userInfoURL: google.URL + "/userinfo",
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM users WHERE email = $1", fmt.Sprintf("%s@example.com", t.Name()))
	})
	return svc
}

func TestLoginURLIncludesState(t *testing.T) {
	svc := newTestService(t, fakeGoogle(t, "sub-1", "someone@example.com"))
	url := svc.LoginURL("my-state")
	if url == "" {
		t.Fatal("LoginURL() returned an empty string")
	}
	if !contains(url, "state=my-state") {
		t.Errorf("LoginURL() = %q, want it to include state=my-state", url)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

func TestHandleCallbackCreatesUserAndSession(t *testing.T) {
	email := t.Name() + "@example.com"
	svc := newTestService(t, fakeGoogle(t, "sub-"+t.Name(), email))
	ctx := context.Background()

	token, err := svc.HandleCallback(ctx, "any-code")
	if err != nil {
		t.Fatalf("HandleCallback() error = %v, want nil", err)
	}
	if token == "" {
		t.Fatal("HandleCallback() returned an empty token")
	}

	user, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate() error = %v, want nil", err)
	}
	if user.Email != email {
		t.Errorf("Authenticate() Email = %q, want %q", user.Email, email)
	}
}

func TestHandleCallbackUpsertsSameUser(t *testing.T) {
	email := t.Name() + "@example.com"
	sub := "sub-" + t.Name()
	svc := newTestService(t, fakeGoogle(t, sub, email))
	ctx := context.Background()

	token1, err := svc.HandleCallback(ctx, "code-1")
	if err != nil {
		t.Fatalf("first HandleCallback() error = %v, want nil", err)
	}
	user1, err := svc.Authenticate(ctx, token1)
	if err != nil {
		t.Fatalf("Authenticate() error = %v, want nil", err)
	}

	token2, err := svc.HandleCallback(ctx, "code-2")
	if err != nil {
		t.Fatalf("second HandleCallback() error = %v, want nil", err)
	}
	user2, err := svc.Authenticate(ctx, token2)
	if err != nil {
		t.Fatalf("Authenticate() error = %v, want nil", err)
	}

	if user1.ID != user2.ID {
		t.Errorf("two logins for the same Google account produced different user IDs: %d vs %d", user1.ID, user2.ID)
	}
}

func TestAuthenticateNoSession(t *testing.T) {
	svc := newTestService(t, fakeGoogle(t, "sub-x", "x@example.com"))

	if _, err := svc.Authenticate(context.Background(), ""); err != ErrNoSession {
		t.Errorf("Authenticate(\"\") error = %v, want ErrNoSession", err)
	}
	if _, err := svc.Authenticate(context.Background(), "not-a-real-token"); err != ErrNoSession {
		t.Errorf("Authenticate(garbage) error = %v, want ErrNoSession", err)
	}
}

func TestAuthenticateExpiredSession(t *testing.T) {
	email := t.Name() + "@example.com"
	svc := newTestService(t, fakeGoogle(t, "sub-"+t.Name(), email))
	ctx := context.Background()

	token, err := svc.HandleCallback(ctx, "any-code")
	if err != nil {
		t.Fatalf("HandleCallback() error = %v, want nil", err)
	}

	// Force this session's expiry into the past.
	sum := tokenHash(token)
	if _, err := svc.db.ExecContext(ctx, "UPDATE sessions SET expires_at = $1 WHERE token_hash = $2", time.Now().Add(-time.Hour), sum); err != nil {
		t.Fatalf("expire session: %v", err)
	}

	if _, err := svc.Authenticate(ctx, token); err != ErrNoSession {
		t.Errorf("Authenticate() on expired session error = %v, want ErrNoSession", err)
	}
}

func TestLogoutInvalidatesSession(t *testing.T) {
	email := t.Name() + "@example.com"
	svc := newTestService(t, fakeGoogle(t, "sub-"+t.Name(), email))
	ctx := context.Background()

	token, err := svc.HandleCallback(ctx, "any-code")
	if err != nil {
		t.Fatalf("HandleCallback() error = %v, want nil", err)
	}
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatalf("Logout() error = %v, want nil", err)
	}
	if _, err := svc.Authenticate(ctx, token); err != ErrNoSession {
		t.Errorf("Authenticate() after Logout() error = %v, want ErrNoSession", err)
	}
}

func TestFromRequestCookie(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: "cookie-token"})
	if got := FromRequest(r); got != "cookie-token" {
		t.Errorf("FromRequest() = %q, want %q", got, "cookie-token")
	}
}

func TestFromRequestBearerHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer header-token")
	if got := FromRequest(r); got != "header-token" {
		t.Errorf("FromRequest() = %q, want %q", got, "header-token")
	}
}

func TestFromRequestNone(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if got := FromRequest(r); got != "" {
		t.Errorf("FromRequest() = %q, want \"\"", got)
	}
}
