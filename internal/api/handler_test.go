package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// testDatabaseURL mirrors internal/db's helper: tests that need a real,
// already-migrated Postgres database skip without one.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SUDOKU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SUDOKU_TEST_DATABASE_URL not set, skipping integration test")
	}
	return url
}

func testHandler() (*Handler, *game.Store) {
	store := game.NewStore()
	lookup := func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		var givens, solution sudoku.Grid
		givens[0][0] = 5
		solution[0][0] = 5
		solution[0][1] = 3
		return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: d}, nil
	}
	return NewHandler(store, lookup, nil, nil), store
}

// testHandlerWithAuth is like testHandler but also wires a real
// auth.Service (backed by sqlDB) so authenticated-request tests can
// exercise the logged-in path end to end.
func testHandlerWithAuth(t *testing.T) (h *Handler, authSvc *auth.Service, sqlDB *sql.DB) {
	t.Helper()
	sqlDB, err := db.Open(testDatabaseURL(t))
	if err != nil {
		t.Fatalf("db.Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	authSvc = auth.NewService(sqlDB, "unused", "unused", "unused")
	store := game.NewStore()
	lookup := func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		var givens, solution sudoku.Grid
		givens[0][0] = 5
		solution[0][0] = 5
		solution[0][1] = 3
		return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: d}, nil
	}
	h = NewHandler(store, lookup, authSvc, sqlDB)
	return h, authSvc, sqlDB
}

// loginTestUser inserts a user + session directly (bypassing the real
// Google flow, which these tests don't exercise) and returns a bearer
// token for it.
func loginTestUser(t *testing.T, sqlDB *sql.DB, email string) string {
	t.Helper()
	ctx := context.Background()
	var userID int64
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
	return token
}

func mustCreate(t *testing.T, store *game.Store, p sudoku.Puzzle) *game.Game {
	t.Helper()
	g, err := store.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("store.Create() error = %v, want nil", err)
	}
	return g
}

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestCreateGame(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"easy", `{"difficulty":"easy"}`, http.StatusCreated},
		{"medium", `{"difficulty":"medium"}`, http.StatusCreated},
		{"hard", `{"difficulty":"hard"}`, http.StatusCreated},
		{"expert", `{"difficulty":"expert"}`, http.StatusCreated},
		{"invalid difficulty", `{"difficulty":"nightmare"}`, http.StatusBadRequest},
		{"missing difficulty", `{}`, http.StatusBadRequest},
		{"invalid body", `not json`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := testHandler()
			mux := newMux(h)

			req := httptest.NewRequest(http.MethodPost, "/api/games", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var state gameState
				if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
					t.Fatalf("unmarshal response: %v", err)
				}
				if state.ID == "" {
					t.Error("response has empty id")
				}
				if state.Solved {
					t.Error("newly created game reports solved = true")
				}
			}
		})
	}
}

func TestGetGame(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/games/"+g.ID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.ID != g.ID {
		t.Errorf("id = %q, want %q", state.ID, g.ID)
	}
}

func TestGetGameNotFound(t *testing.T) {
	h, _ := testHandler()
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/games/does-not-exist", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSubmitMove(t *testing.T) {
	h, store := testHandler()
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3 // != givens[0][1] (0), so the game isn't already solved at creation
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":0,"col":1,"value":7}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.Current[0][1] != 7 {
		t.Errorf("current[0][1] = %d, want 7", state.Current[0][1])
	}
}

func TestSubmitMoveOnGivenCell(t *testing.T) {
	h, store := testHandler()
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3 // != givens[0][1] (0), so the game isn't already solved at creation
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":0,"col":0,"value":9}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSubmitMoveOutOfRange(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":9,"col":0,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSubmitMoveUnknownGame(t *testing.T) {
	h, _ := testHandler()
	mux := newMux(h)

	body := `{"row":0,"col":0,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/does-not-exist/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSubmitMoveWrongValueIncrementsMistakes(t *testing.T) {
	h, store := testHandler()
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3 // != givens[0][1] (0), so the game isn't already solved at creation
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy})

	body := `{"row":0,"col":1,"value":9}` // wrong: solution wants 3 here
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux := newMux(h)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.Mistakes != 1 {
		t.Errorf("mistakes = %d, want 1", state.Mistakes)
	}
	if state.MaxMistakes != 5 {
		t.Errorf("maxMistakes = %d, want 5 for Easy", state.MaxMistakes)
	}
	if state.Failed {
		t.Errorf("failed = true after 1 of 5 mistakes, want false")
	}
}

func TestSubmitMoveAfterGameOver(t *testing.T) {
	h, store := testHandler()
	var givens, solution sudoku.Grid
	solution[0][1] = 3                                                                                    // != givens[0][1] (0), so the game isn't already solved at creation
	g := mustCreate(t, store, sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Hard}) // MaxMistakes == 3
	mux := newMux(h)

	for i := 0; i < 3; i++ {
		body := `{"row":0,"col":1,"value":9}` // always wrong
		req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("mistake %d: status = %d, want %d (body: %s)", i+1, rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	body := `{"row":0,"col":2,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status after game over = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestCreateGameUsesDBStoreWhenAuthenticated(t *testing.T) {
	h, _, sqlDB := testHandlerWithAuth(t)
	token := loginTestUser(t, sqlDB, "api-create-"+t.Name()+"@example.com")

	req := httptest.NewRequest("POST", "/api/games", bytes.NewBufferString(`{"difficulty":"easy"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM games WHERE id = $1", state.ID)
	})

	// Scoped to this test's own game id, not a table-wide count: a
	// table-wide SELECT/DELETE here would give false failures against a
	// database with other rows already in it, and would destroy any
	// developer's real local game data if run against their dev DB.
	var count int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM games WHERE id = $1", state.ID).Scan(&count); err != nil {
		t.Fatalf("query games count: %v", err)
	}
	if count == 0 {
		t.Error("no row was written to the games table for an authenticated create")
	}
}

func TestCreateGameAnonymousDoesNotTouchDB(t *testing.T) {
	h, _, sqlDB := testHandlerWithAuth(t)

	// Anonymous play never gets its own row to scope by (that's the
	// point being tested), so this compares a before/after count of the
	// whole table instead of asserting it's 0 outright or deleting from
	// it: that avoids both a false failure against a database that
	// already has other rows in it, and destroying any developer's real
	// local game data if this suite is run against their dev DB.
	var before int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM games").Scan(&before); err != nil {
		t.Fatalf("query games count before: %v", err)
	}

	req := httptest.NewRequest("POST", "/api/games", bytes.NewBufferString(`{"difficulty":"easy"}`))
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var after int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM games").Scan(&after); err != nil {
		t.Fatalf("query games count after: %v", err)
	}
	if after != before {
		t.Errorf("games table row count changed from %d to %d after an anonymous create, want unchanged", before, after)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	h, _, sqlDB := testHandlerWithAuth(t)
	token := loginTestUser(t, sqlDB, "api-logout-"+t.Name()+"@example.com")

	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	var count int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM sessions WHERE token_hash = $1", auth.TokenHashForTest(token)).Scan(&count); err != nil {
		t.Fatalf("query sessions: %v", err)
	}
	if count != 0 {
		t.Error("session row still exists after logout")
	}
}

// TestAuthFallbackToAnon exercises storeFor's error-classification
// logic in isolation. Triggering a genuine non-ErrNoSession failure out
// of a real auth.Service.Authenticate would need fault injection into
// the database connection, which isn't practical here — this unit test
// instead pins the classification rule storeFor depends on: only
// auth.ErrNoSession means "fall back to anonymous," anything else must
// be propagated as a real error.
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
