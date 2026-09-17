# Phase 3 (Accounts and Persistence) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Google OAuth login and persistent, resumable games for logged-in users, while anonymous play keeps working exactly as it does today (in-memory, not persisted).

**Architecture:** A new `game.GameStore` interface (`Create`/`Get`/`ApplyMove`) is implemented by both the existing in-memory `game.Store` (anonymous play) and a new Postgres-backed `db.GameStore` (logged-in play, one row per game, one active row per `user_id`+`difficulty`). A new `internal/auth` package handles the Google OAuth dance and opaque session tokens. `internal/api` and `internal/web` each pick which `GameStore` implementation to use per request based on whether the request carries a valid session.

**Tech Stack:** Go 1.27, `database/sql` + `github.com/jackc/pgx/v5`, `golang-migrate`, `golang.org/x/oauth2` (new dependency), `net/http`, `html/template`.

**Spec:** `docs/superpowers/specs/2026-09-17-phase-3-accounts-design.md`

## Global Constraints

- Google OAuth only for this phase (no GitHub) — spec Non-goals.
- No ORM; hand-written SQL in `internal/db`, grids stored via the existing `gridToString`/`stringToGrid` helpers (81-char strings), matching the `puzzles` table convention (see `migrations/0001_create_puzzles.up.sql`) — not JSONB.
- `internal/sudoku` stays dependency-free and untouched by this phase.
- `internal/game`'s move-validation rules must live in exactly one place, used by both `GameStore` implementations.
- Anonymous play behavior and API/web response shapes for existing endpoints are unchanged.
- One active (`in_progress`) game per `(user_id, difficulty)`, enforced by a partial unique index, not just application logic.
- Session tokens are opaque (crypto/rand), only their SHA-256 hash is ever stored.
- Follow `AGENTS.md`'s git workflow: all work stays on the `phase-3-accounts` branch; never merge to `main` unless the user explicitly asks.
- Run `go test ./...` (and `gofmt -l .`) before considering any task done, per `AGENTS.md` testing expectations.

---

## Task 1: Config additions for Google OAuth

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `.env` (add `GOOGLE_REDIRECT_URL`; `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` already present)
- Modify: `.env.example`

**Interfaces:**
- Produces: `config.Config` gains `GoogleClientID`, `GoogleClientSecret`, `GoogleRedirectURL string` fields, all loaded via the existing `getEnv` pattern with `""` fallback.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestLoadGoogleOAuthFromEnv(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "client-id")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback")

	cfg := Load()

	if cfg.GoogleClientID != "client-id" {
		t.Errorf("GoogleClientID = %q, want %q", cfg.GoogleClientID, "client-id")
	}
	if cfg.GoogleClientSecret != "client-secret" {
		t.Errorf("GoogleClientSecret = %q, want %q", cfg.GoogleClientSecret, "client-secret")
	}
	if cfg.GoogleRedirectURL != "http://localhost:8080/auth/google/callback" {
		t.Errorf("GoogleRedirectURL = %q, want %q", cfg.GoogleRedirectURL, "http://localhost:8080/auth/google/callback")
	}
}

func TestLoadGoogleOAuthDefaultsEmpty(t *testing.T) {
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("GOOGLE_CLIENT_SECRET", "")
	t.Setenv("GOOGLE_REDIRECT_URL", "")

	cfg := Load()

	if cfg.GoogleClientID != "" || cfg.GoogleClientSecret != "" || cfg.GoogleRedirectURL != "" {
		t.Errorf("Google fields = %q/%q/%q, want all empty when unset", cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/... -run Google -v`
Expected: FAIL — `cfg.GoogleClientID undefined (type Config has no field or method GoogleClientID)`

- [ ] **Step 3: Implement**

Replace the contents of `internal/config/config.go`'s `Config` struct and `Load`:

```go
type Config struct {
	Port               string
	DatabaseURL        string
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
}

func Load() Config {
	return Config{
		Port:               getEnv("PORT", "8080"),
		DatabaseURL:        getEnv("DATABASE_URL", ""),
		GoogleClientID:     getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:  getEnv("GOOGLE_REDIRECT_URL", ""),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/... -v`
Expected: PASS (all tests, including the two pre-existing ones)

- [ ] **Step 5: Add the redirect URL to local env files**

Add to `.env` (keep the existing `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` lines as-is):

```
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
```

Add to `.env.example` (placeholders, no real values):

```
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=
GOOGLE_REDIRECT_URL=http://localhost:8080/auth/google/callback
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go .env.example
git commit -m "Add Google OAuth config fields"
```

(`.env` is expected to already be gitignored — confirm with `git status` that it doesn't appear before committing; if it does, stop and flag it instead of committing it.)

---

## Task 2: `users`/`sessions`/`games` migration

**Files:**
- Create: `migrations/0002_accounts.up.sql`
- Create: `migrations/0002_accounts.down.sql`

**Interfaces:**
- Produces: tables `users`, `sessions`, `games` for Tasks 4 and 5 to query against.

- [ ] **Step 1: Write the up migration**

`migrations/0002_accounts.up.sql`:

```sql
CREATE TABLE users (
    id               BIGSERIAL PRIMARY KEY,
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    email            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE games (
    id           TEXT PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    difficulty   SMALLINT NOT NULL,
    givens       CHAR(81) NOT NULL,
    current      CHAR(81) NOT NULL,
    solution     CHAR(81) NOT NULL,
    mistakes     INT NOT NULL DEFAULT 0,
    max_mistakes INT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'in_progress',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX games_one_active_per_difficulty
    ON games (user_id, difficulty)
    WHERE status = 'in_progress';
```

- [ ] **Step 2: Write the down migration**

`migrations/0002_accounts.down.sql`:

```sql
DROP INDEX IF EXISTS games_one_active_per_difficulty;
DROP TABLE IF EXISTS games;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
```

- [ ] **Step 3: Verify up and down against a real database**

Run (requires Postgres running, e.g. `make up`, and `DATABASE_URL` set — see README):

```bash
go run ./cmd/migrate up
go run ./cmd/migrate down
go run ./cmd/migrate up
```

Expected: each command prints `migration version: <n> (dirty=false)` with no error; after the final `up`, connect with `psql "$DATABASE_URL" -c '\dt'` and confirm `users`, `sessions`, `games` (and `puzzles`, `schema_migrations`) all exist.

- [ ] **Step 4: Commit**

```bash
git add migrations/0002_accounts.up.sql migrations/0002_accounts.down.sql
git commit -m "Add users, sessions, and games tables"
```

---

## Task 3: `internal/game` — `GameStore` interface and shared move logic

**Files:**
- Modify: `internal/game/game.go`
- Modify: `internal/game/game_test.go`
- Modify: `internal/api/handler.go` (adapt call sites to new signatures only — no auth yet)
- Modify: `internal/api/handler_test.go` (adapt call sites only)
- Modify: `internal/web/web.go` (adapt call sites only)

**Interfaces:**
- Produces:
  - `func ApplyMove(g *Game, row, col, value int) error` (exported, pure — the single place all move-validation rules live)
  - `func NewID() string` (exported, was `newID`)
  - `func MaxMistakesFor(d sudoku.Difficulty) int` (exported, was `maxMistakesFor`)
  - `type GameStore interface { Create(ctx, sudoku.Puzzle) (*Game, error); Get(ctx, id string) (*Game, error); ApplyMove(ctx, id string, row, col, value int) (*Game, error) }`
  - `*Store` implements `GameStore` (previously `Get` returned `(*Game, bool)`; now `(*Game, error)` with `ErrNotFound`. `Create` previously returned `*Game` only; now `(*Game, error)`, always `nil` error for the in-memory store.)
- Consumes: nothing new from other packages.

- [ ] **Step 1: Write the failing tests (full replacement of `game_test.go`)**

Replace the entire contents of `internal/game/game_test.go`:

```go
package game

import (
	"context"
	"sync"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

var ctx = context.Background()

func testPuzzle() sudoku.Puzzle {
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3
	return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy}
}

func mustCreate(t *testing.T, s *Store, p sudoku.Puzzle) *Game {
	t.Helper()
	g, err := s.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	return g
}

func TestStoreCreateAndGet(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if g.ID == "" {
		t.Fatal("Create() returned a game with an empty ID")
	}
	if g.Current != g.Givens {
		t.Errorf("Current = %v, want equal to Givens on creation", g.Current)
	}

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", g.ID, err)
	}
	if *got != *g {
		t.Errorf("Get(%q) returned different game data than Create produced", g.ID)
	}
}

func TestStoreCreateGeneratesUniqueIDs(t *testing.T) {
	s := NewStore()
	g1 := mustCreate(t, s, testPuzzle())
	g2 := mustCreate(t, s, testPuzzle())

	if g1.ID == g2.ID {
		t.Errorf("two Create() calls produced the same ID %q", g1.ID)
	}
}

func TestStoreGetUnknownID(t *testing.T) {
	s := NewStore()
	if _, err := s.Get(ctx, "does-not-exist"); err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestStoreApplyMove(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Current[0][1] != 3 {
		t.Errorf("Current[0][1] = %d, want 3", updated.Current[0][1])
	}

	updated, err = s.ApplyMove(ctx, g.ID, 0, 1, 0)
	if err != nil {
		t.Fatalf("ApplyMove() clearing cell error = %v, want nil", err)
	}
	if updated.Current[0][1] != 0 {
		t.Errorf("Current[0][1] = %d, want 0 after clearing", updated.Current[0][1])
	}
}

func TestStoreApplyMoveOutOfRange(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	tests := []struct {
		name            string
		row, col, value int
	}{
		{"row too low", -1, 0, 1},
		{"row too high", 9, 0, 1},
		{"col too low", 0, -1, 1},
		{"col too high", 0, 9, 1},
		{"value too low", 0, 1, -1},
		{"value too high", 0, 1, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ApplyMove(ctx, g.ID, tt.row, tt.col, tt.value); err != ErrOutOfRange {
				t.Errorf("ApplyMove(%d,%d,%d) error = %v, want ErrOutOfRange", tt.row, tt.col, tt.value, err)
			}
		})
	}
}

func TestStoreApplyMoveGivenCell(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // (0,0) is a given (value 5)

	if _, err := s.ApplyMove(ctx, g.ID, 0, 0, 7); err != ErrGivenCell {
		t.Errorf("ApplyMove() on a given cell error = %v, want ErrGivenCell", err)
	}
}

func TestStoreApplyMoveUnknownGame(t *testing.T) {
	s := NewStore()
	if _, err := s.ApplyMove(ctx, "does-not-exist", 0, 1, 5); err != ErrNotFound {
		t.Errorf("ApplyMove() on unknown game error = %v, want ErrNotFound", err)
	}
}

func TestGameSolved(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if g.Solved() {
		t.Error("Solved() = true immediately after creation, want false")
	}

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if !updated.Solved() {
		t.Error("Solved() = false after Current matches Solution, want true")
	}
}

func TestGetReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())
	gameID := g.ID

	copy1, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", gameID, err)
	}

	copy1.Current[0][1] = 9
	copy1.Current[5][5] = 7

	copy2, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v after first mutation, want nil", gameID, err)
	}

	if copy2.Current[0][1] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[0][1] = %d, want 0", copy2.Current[0][1])
	}
	if copy2.Current[5][5] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[5][5] = %d, want 0", copy2.Current[5][5])
	}
}

func TestCreateReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())
	gameID := g.ID

	g.Current[0][1] = 9
	g.Current[5][5] = 7

	got, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", gameID, err)
	}
	if got.Current[0][1] != 0 {
		t.Errorf("After mutating Create's returned copy, store's copy was affected: Current[0][1] = %d, want 0", got.Current[0][1])
	}
	if got.Current[5][5] != 0 {
		t.Errorf("After mutating Create's returned copy, store's copy was affected: Current[5][5] = %d, want 0", got.Current[5][5])
	}
}

func TestApplyMoveReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}

	updated.Current[0][1] = 9
	updated.Current[5][5] = 7

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", g.ID, err)
	}
	if got.Current[0][1] != 3 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[0][1] = %d, want 3", got.Current[0][1])
	}
	if got.Current[5][5] != 0 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[5][5] = %d, want 0", got.Current[5][5])
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()

	const sharedGames = 4
	shared := make([]*Game, sharedGames)
	for i := range shared {
		shared[i] = mustCreate(t, s, testPuzzle())
	}

	const workers = 20
	const iterations = 50

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			sharedGame := shared[worker%sharedGames]
			for j := 0; j < iterations; j++ {
				g, err := s.Create(ctx, testPuzzle())
				if err != nil {
					t.Errorf("Create() error = %v, want nil", err)
					continue
				}

				if _, err := s.Get(ctx, g.ID); err != nil {
					t.Errorf("Get(%q) error = %v for a game just created, want nil", g.ID, err)
				}

				if _, err := s.ApplyMove(ctx, g.ID, 0, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", g.ID, err)
				}

				if _, err := s.Get(ctx, sharedGame.ID); err != nil {
					t.Errorf("Get(%q) error = %v for shared game, want nil", sharedGame.ID, err)
				}
				if _, err := s.ApplyMove(ctx, sharedGame.ID, 1, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", sharedGame.ID, err)
				}
			}
		}(i)
	}
	wg.Wait()

	for _, g := range shared {
		got, err := s.Get(ctx, g.ID)
		if err != nil {
			t.Errorf("Get(%q) error = %v after concurrent access, want nil", g.ID, err)
			continue
		}
		if got.Current[1][1] == 0 {
			t.Errorf("shared game %q Current[1][1] = 0 after concurrent ApplyMove calls, want a value written by some goroutine", g.ID)
		}
	}
}

func TestCreateSetsMaxMistakesByDifficulty(t *testing.T) {
	tests := []struct {
		d    sudoku.Difficulty
		want int
	}{
		{sudoku.Easy, 5},
		{sudoku.Medium, 5},
		{sudoku.Hard, 3},
		{sudoku.Expert, 3},
	}
	for _, tt := range tests {
		p := testPuzzle()
		p.Difficulty = tt.d
		s := NewStore()
		g := mustCreate(t, s, p)
		if g.MaxMistakes != tt.want {
			t.Errorf("Create() with difficulty %v: MaxMistakes = %d, want %d", tt.d, g.MaxMistakes, tt.want)
		}
	}
}

func TestApplyMoveWrongValueCountsMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 9) // wrong: solution wants 3
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1 after one wrong entry", updated.Mistakes)
	}
	if updated.Current[0][1] != 9 {
		t.Errorf("Current[0][1] = %d, want 9 (wrong entries still fill the cell)", updated.Current[0][1])
	}
}

func TestApplyMoveCorrectValueDoesNotCountMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3) // correct
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 0 {
		t.Errorf("Mistakes = %d, want 0 after a correct entry", updated.Mistakes)
	}
}

func TestApplyMoveClearingCellDoesNotCountMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 9); err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 0) // erase
	if err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1 (erasing should not add a mistake, and should not remove the earlier one)", updated.Mistakes)
	}
}

func TestApplyMoveRejectedAfterGameOver(t *testing.T) {
	s := NewStore()
	p := testPuzzle()
	p.Difficulty = sudoku.Hard // MaxMistakes == 3
	g := mustCreate(t, s, p)

	for i := 0; i < 3; i++ {
		if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 9); err != nil { // always wrong
			t.Fatalf("ApplyMove() error = %v on mistake %d", err, i+1)
		}
	}

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if !got.Failed() {
		t.Fatalf("Failed() = false after 3 mistakes with MaxMistakes=3, want true")
	}

	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 3); err != ErrGameOver {
		t.Errorf("ApplyMove() after game over: error = %v, want ErrGameOver", err)
	}
}

// compile-time check that *Store implements GameStore.
var _ GameStore = (*Store)(nil)
```

- [ ] **Step 2: Run tests to verify they fail to compile**

Run: `go test ./internal/game/...`
Expected: build failure (`too many arguments in call to s.Get`, `GameStore undefined`, etc.)

- [ ] **Step 3: Implement — replace `internal/game/game.go`**

```go
// Package game holds the in-memory game session store shared by both
// the REST API and the htmx web UI, per AGENTS.md's rule that they must
// use the same service layer rather than diverging.
package game

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// Game is one in-progress (or completed) puzzle session.
type Game struct {
	ID          string
	Givens      sudoku.Grid // original clues; never mutated after creation
	Current     sudoku.Grid // the player's working grid
	Solution    sudoku.Grid
	Difficulty  sudoku.Difficulty
	Mistakes    int
	MaxMistakes int
}

// Solved reports whether Current matches Solution exactly.
func (g *Game) Solved() bool {
	return g.Current == g.Solution
}

// Failed reports whether the player has used up all their allowed
// mistakes for this game.
func (g *Game) Failed() bool {
	return g.Mistakes >= g.MaxMistakes
}

// MaxMistakesFor returns the mistake allowance for a given difficulty:
// 5 for Easy/Medium, 3 for Hard/Expert.
func MaxMistakesFor(d sudoku.Difficulty) int {
	if d == sudoku.Hard || d == sudoku.Expert {
		return 3
	}
	return 5
}

var (
	ErrOutOfRange = errors.New("row/col must be 0-8 and value must be 0-9")
	ErrGivenCell  = errors.New("cannot change a given cell")
	ErrNotFound   = errors.New("game not found")
	ErrGameOver   = errors.New("game is over: mistake limit reached")
)

// GameStore is the interface both the in-memory Store (anonymous play)
// and the Postgres-backed db.GameStore (logged-in play) implement, so
// internal/api and internal/web can depend on the interface and pick an
// implementation per request without duplicating move-handling code.
type GameStore interface {
	Create(ctx context.Context, p sudoku.Puzzle) (*Game, error)
	Get(ctx context.Context, id string) (*Game, error)
	ApplyMove(ctx context.Context, id string, row, col, value int) (*Game, error)
}

// ApplyMove mutates g in place according to a player move (0 clears the
// cell) and returns the sentinel error to reject it with, or nil on
// success. value must be 0-9; row and col must be 0-8. A cell that is
// non-zero in g.Givens cannot be changed. Placing a non-zero value that
// doesn't match g.Solution still fills the cell (so the player can see
// what they entered) but counts as a mistake. No further moves are
// accepted once g.Failed() is already true. This is the single place
// move-validation rules live — both GameStore implementations call it
// after loading their own copy of the Game.
func ApplyMove(g *Game, row, col, value int) error {
	if row < 0 || row > 8 || col < 0 || col > 8 || value < 0 || value > 9 {
		return ErrOutOfRange
	}
	if g.Failed() {
		return ErrGameOver
	}
	if g.Givens[row][col] != 0 {
		return ErrGivenCell
	}
	if value != 0 && value != g.Solution[row][col] {
		g.Mistakes++
	}
	g.Current[row][col] = value
	return nil
}

// Store holds all in-progress games in memory, safe for concurrent use.
// It implements GameStore and backs anonymous (not-logged-in) play.
type Store struct {
	mu    sync.Mutex
	games map[string]*Game
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{games: make(map[string]*Game)}
}

// Create starts a new Game from a freshly pulled puzzle and returns it.
// Returns an independent copy to prevent data races from concurrent
// access. The context is accepted to satisfy GameStore; the in-memory
// store never uses it.
func (s *Store) Create(ctx context.Context, p sudoku.Puzzle) (*Game, error) {
	g := &Game{
		ID:          NewID(),
		Givens:      p.Givens,
		Current:     p.Givens,
		Solution:    p.Solution,
		Difficulty:  p.Difficulty,
		MaxMistakes: MaxMistakesFor(p.Difficulty),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games[g.ID] = g
	result := *g
	return &result, nil
}

// Get returns the game with the given ID, or ErrNotFound if it doesn't
// exist. Returns an independent copy to prevent data races from
// concurrent access.
func (s *Store) Get(ctx context.Context, id string) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.games[id]
	if !ok {
		return nil, ErrNotFound
	}
	copy := *g
	return &copy, nil
}

// ApplyMove applies a move to the game with the given id via the shared
// ApplyMove rules and returns the updated Game. Returns ErrNotFound if
// id doesn't exist. Returns an independent copy to prevent data races
// from concurrent access.
func (s *Store) ApplyMove(ctx context.Context, id string, row, col, value int) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.games[id]
	if !ok {
		return nil, ErrNotFound
	}
	if err := ApplyMove(g, row, col, value); err != nil {
		return nil, err
	}
	copy := *g
	return &copy, nil
}

// NewID returns a random, opaque, unguessable-enough identifier, used
// both for game IDs and (by internal/auth) session tokens.
// crypto/rand.Read failing indicates the environment itself is broken
// (no source of randomness available); there is no sane recovery, so
// this panics rather than returning a predictable or empty ID.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("game: failed to read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// PuzzleLookup returns a random puzzle of difficulty d. Production code
// passes db.RandomPuzzle (adapted to this signature); tests pass a stub.
type PuzzleLookup func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error)
```

- [ ] **Step 4: Run `internal/game` tests to verify they pass**

Run: `go test ./internal/game/... -race -v`
Expected: PASS (all tests)

- [ ] **Step 5: Fix compile in `internal/api/handler.go`**

In `internal/api/handler.go`, update the three call sites:

```go
func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d, ok := game.ParseDifficulty(req.Difficulty)
	if !ok {
		writeError(w, http.StatusBadRequest, "difficulty must be one of easy, medium, hard, expert")
		return
	}

	p, err := h.puzzles(r.Context(), d)
	if err != nil {
		log.Printf("api: puzzle lookup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not find a puzzle right now")
		return
	}

	g, err := h.store.Create(r.Context(), p)
	if err != nil {
		log.Printf("api: create game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create game")
		return
	}
	writeJSON(w, http.StatusCreated, toGameState(g))
}

func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := h.store.Get(r.Context(), id)
	if errors.Is(err, game.ErrNotFound) {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	if err != nil {
		log.Printf("api: get game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load game")
		return
	}
	writeJSON(w, http.StatusOK, toGameState(g))
}
```

And change `submitMove`'s call from `h.store.ApplyMove(id, req.Row, req.Col, req.Value)` to `h.store.ApplyMove(r.Context(), id, req.Row, req.Col, req.Value)` (its error handling via `errors.Is` already fits).

Change the `Handler` struct's `store` field type and `NewHandler`'s parameter type from `*game.Store` to `game.GameStore`:

```go
type Handler struct {
	store   game.GameStore
	puzzles game.PuzzleLookup
}

func NewHandler(store game.GameStore, puzzles game.PuzzleLookup) *Handler {
	return &Handler{store: store, puzzles: puzzles}
}
```

- [ ] **Step 6: Fix compile in `internal/api/handler_test.go`**

`testHandler` still passes a `*game.Store` (which satisfies `game.GameStore`), so it compiles unchanged — only check that no test asserts on `ok`/two-return-value `Get`/`Create` directly (they don't; they only go through the HTTP handlers). No change needed beyond re-running the tests.

- [ ] **Step 7: Fix compile in `internal/web/web.go`**

Update the struct/constructor the same way:

```go
type Handler struct {
	store   game.GameStore
	puzzles game.PuzzleLookup
}

func NewHandler(store game.GameStore, puzzles game.PuzzleLookup) *Handler {
	return &Handler{store: store, puzzles: puzzles}
}
```

Update `createGame`:

```go
func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	d, ok := game.ParseDifficulty(r.FormValue("difficulty"))
	if !ok {
		http.Error(w, "invalid difficulty", http.StatusBadRequest)
		return
	}

	p, err := h.puzzles(r.Context(), d)
	if err != nil {
		log.Printf("web: puzzle lookup failed: %v", err)
		http.Error(w, "could not find a puzzle right now", http.StatusInternalServerError)
		return
	}

	g, err := h.store.Create(r.Context(), p)
	if err != nil {
		log.Printf("web: create game failed: %v", err)
		http.Error(w, "could not create game", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/play/"+g.ID, http.StatusFound)
}
```

Update `showBoard`:

```go
func (h *Handler) showBoard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := h.store.Get(r.Context(), id)
	if errors.Is(err, game.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("web: get game failed: %v", err)
		http.Error(w, "could not load game", http.StatusInternalServerError)
		return
	}
	if err := templates.ExecuteTemplate(w, "boardPage", newBoardView(g)); err != nil {
		log.Printf("web: render board page: %v", err)
	}
}
```

And change `submitMove`'s call from `h.store.ApplyMove(id, row, col, value)` to `h.store.ApplyMove(r.Context(), id, row, col, value)`.

- [ ] **Step 8: Run the whole suite**

Run: `go test ./... -race`
Expected: PASS across the board; `go build ./...` also succeeds.

- [ ] **Step 9: Commit**

```bash
git add internal/game/game.go internal/game/game_test.go internal/api/handler.go internal/web/web.go
git commit -m "Extract GameStore interface and shared move-validation logic"
```

---

## Task 4: `db.GameStore` — Postgres-backed game persistence

**Files:**
- Create: `internal/db/games.go`
- Create: `internal/db/games_test.go`

**Interfaces:**
- Consumes: `game.Game`, `game.GameStore`, `game.ApplyMove`, `game.MaxMistakesFor`, `game.NewID`, `game.ErrNotFound` (Task 3); `gridToString`/`stringToGrid` (existing, same package).
- Produces: `func NewGameStore(sqlDB *sql.DB, userID int64) *GameStore`, implementing `game.GameStore`.

- [ ] **Step 1: Write the failing tests**

`internal/db/games_test.go`:

```go
package db

import (
	"context"
	"testing"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// testUser inserts a fresh user row and returns its id, cleaning up
// after the test (cascades to sessions/games via ON DELETE CASCADE).
func testUser(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	providerUserID := gameTestID(t)
	err := sqlDB.QueryRowContext(ctx,
		"INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $2) RETURNING id",
		providerUserID, providerUserID+"@example.com",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
			t.Logf("cleanup test user: %v", err)
		}
	})
	return id
}

// gameTestID returns a short random string, distinct per call, for use
// as a unique fixture key (provider_user_id here) so parallel test runs
// never collide.
func gameTestID(t *testing.T) string {
	t.Helper()
	return game.NewID()
}

func testGamePuzzle() sudoku.Puzzle {
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3
	return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy}
}

func TestGameStoreCreateAndGet(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	g, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if g.ID == "" {
		t.Fatal("Create() returned a game with an empty ID")
	}
	if g.MaxMistakes != game.MaxMistakesFor(sudoku.Easy) {
		t.Errorf("MaxMistakes = %d, want %d", g.MaxMistakes, game.MaxMistakesFor(sudoku.Easy))
	}

	got, err := store.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if *got != *g {
		t.Errorf("Get() returned %+v, want %+v", got, g)
	}
}

func TestGameStoreGetUnknownID(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	if _, err := store.Get(context.Background(), "does-not-exist"); err != game.ErrNotFound {
		t.Errorf("Get() error = %v, want game.ErrNotFound", err)
	}
}

func TestGameStoreGetScopedToUser(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	owner := testUser(t, sqlDB)
	other := testUser(t, sqlDB)

	g, err := NewGameStore(sqlDB, owner).Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := NewGameStore(sqlDB, other).Get(ctx, g.ID); err != game.ErrNotFound {
		t.Errorf("Get() by a different user error = %v, want game.ErrNotFound", err)
	}
}

func TestGameStoreCreateResumesInProgressGame(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	first, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("first Create() error = %v, want nil", err)
	}
	second, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("second Create() error = %v, want nil", err)
	}

	if first.ID != second.ID {
		t.Errorf("second Create() for the same difficulty returned a new game %q, want the existing in-progress game %q", second.ID, first.ID)
	}
}

func TestGameStoreCreateStartsNewGameAfterCompletion(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	first, err := store.Create(ctx, testGamePuzzle()) // solution[0][1] == 3
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := store.ApplyMove(ctx, first.ID, 0, 1, 3); err != nil { // solves it
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}

	second, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("second Create() error = %v, want nil", err)
	}
	if second.ID == first.ID {
		t.Error("Create() after completing the previous game returned the same game, want a new one")
	}
}

func TestGameStoreApplyMove(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	g, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	updated, err := store.ApplyMove(ctx, g.ID, 0, 1, 9) // wrong: solution wants 3
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1", updated.Mistakes)
	}
	if updated.Current[0][1] != 9 {
		t.Errorf("Current[0][1] = %d, want 9", updated.Current[0][1])
	}

	// Persisted, not just returned: a fresh Get sees the same state.
	got, err := store.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if got.Mistakes != 1 || got.Current[0][1] != 9 {
		t.Errorf("Get() after ApplyMove = %+v, want Mistakes=1, Current[0][1]=9", got)
	}
}

func TestGameStoreApplyMoveUnknownGame(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	if _, err := store.ApplyMove(context.Background(), "does-not-exist", 0, 1, 5); err != game.ErrNotFound {
		t.Errorf("ApplyMove() error = %v, want game.ErrNotFound", err)
	}
}

var _ game.GameStore = (*GameStore)(nil)
```

Note: `testUser` above needs `"database/sql"` imported as `*sql.DB` — add `"database/sql"` to the import block.

- [ ] **Step 2: Run tests to verify they fail**

Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/db/... -run TestGameStore -v`
Expected: build failure (`NewGameStore undefined`) — if `SUDOKU_TEST_DATABASE_URL` isn't set, `export SUDOKU_TEST_DATABASE_URL="$DATABASE_URL"` first (requires Postgres running and migrated per Task 2's Step 3).

- [ ] **Step 3: Implement `internal/db/games.go`**

```go
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// GameStore is a game.GameStore backed by the games table, scoped to one
// user. Used for logged-in play; anonymous play uses game.Store instead.
type GameStore struct {
	db     *sql.DB
	userID int64
}

// NewGameStore returns a GameStore for the given user.
func NewGameStore(sqlDB *sql.DB, userID int64) *GameStore {
	return &GameStore{db: sqlDB, userID: userID}
}

// Create returns the user's existing in_progress game for p.Difficulty,
// if any (implementing "resume instead of restart"), otherwise inserts
// and returns a new one.
func (s *GameStore) Create(ctx context.Context, p sudoku.Puzzle) (*game.Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := scanGame(tx.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE user_id = $1 AND difficulty = $2 AND status = 'in_progress'`,
		s.userID, int(p.Difficulty)))
	if err == nil {
		return existing, nil // no writes made; safe to let defer roll back
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("query existing game: %w", err)
	}

	g := &game.Game{
		ID:          game.NewID(),
		Givens:      p.Givens,
		Current:     p.Givens,
		Solution:    p.Solution,
		Difficulty:  p.Difficulty,
		MaxMistakes: game.MaxMistakesFor(p.Difficulty),
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'in_progress')`,
		g.ID, s.userID, int(g.Difficulty), gridToString(g.Givens), gridToString(g.Current), gridToString(g.Solution), g.Mistakes, g.MaxMistakes)
	if err != nil {
		return nil, fmt.Errorf("insert game: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return g, nil
}

// Get returns the user's game with the given id, or game.ErrNotFound if
// it doesn't exist (including if it belongs to a different user).
func (s *GameStore) Get(ctx context.Context, id string) (*game.Game, error) {
	g, err := scanGame(s.db.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE id = $1 AND user_id = $2`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, game.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query game: %w", err)
	}
	return g, nil
}

// ApplyMove loads the user's game with the given id, applies the move
// via the shared game.ApplyMove rules, persists the result, and returns
// the updated Game.
func (s *GameStore) ApplyMove(ctx context.Context, id string, row, col, value int) (*game.Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	g, err := scanGame(tx.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, game.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query game for update: %w", err)
	}

	if err := game.ApplyMove(g, row, col, value); err != nil {
		return nil, err
	}

	status := "in_progress"
	switch {
	case g.Solved():
		status = "solved"
	case g.Failed():
		status = "failed"
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE games SET current = $1, mistakes = $2, status = $3, updated_at = now() WHERE id = $4`,
		gridToString(g.Current), g.Mistakes, status, id)
	if err != nil {
		return nil, fmt.Errorf("update game: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return g, nil
}

// scanGame scans a single games row (as selected by the queries above,
// which all select the same six columns in the same order) into a
// *game.Game.
func scanGame(row *sql.Row) (*game.Game, error) {
	var g game.Game
	var givensStr, currentStr, solutionStr string
	var difficulty int
	if err := row.Scan(&g.ID, &givensStr, &currentStr, &solutionStr, &difficulty, &g.Mistakes, &g.MaxMistakes); err != nil {
		return nil, err
	}
	givens, err := stringToGrid(givensStr)
	if err != nil {
		return nil, fmt.Errorf("parse givens: %w", err)
	}
	current, err := stringToGrid(currentStr)
	if err != nil {
		return nil, fmt.Errorf("parse current: %w", err)
	}
	solution, err := stringToGrid(solutionStr)
	if err != nil {
		return nil, fmt.Errorf("parse solution: %w", err)
	}
	g.Givens, g.Current, g.Solution = givens, current, solution
	g.Difficulty = sudoku.Difficulty(difficulty)
	return &g, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/db/... -v`
Expected: PASS (all `TestGameStore*` tests, plus the pre-existing puzzle tests)

- [ ] **Step 5: Commit**

```bash
git add internal/db/games.go internal/db/games_test.go
git commit -m "Add Postgres-backed GameStore for logged-in play"
```

---

## Task 5: `internal/auth` — Google OAuth and sessions

**Files:**
- Create: `internal/auth/service.go`
- Create: `internal/auth/service_test.go`
- Delete: `internal/auth/doc.go` (its package doc comment moves to the top of `service.go`)
- Modify: `go.mod` (add `golang.org/x/oauth2`)

**Interfaces:**
- Consumes: `*sql.DB` (via `db.Open`, in tests).
- Produces:
  - `type User struct { ID int64; Email string }`
  - `var ErrNoSession = errors.New(...)`
  - `const CookieName = "session"`
  - `func NewService(sqlDB *sql.DB, clientID, clientSecret, redirectURL string) *Service`
  - `func (s *Service) LoginURL(state string) string`
  - `func (s *Service) HandleCallback(ctx context.Context, code string) (token string, err error)`
  - `func (s *Service) Authenticate(ctx context.Context, token string) (*User, error)`
  - `func (s *Service) Logout(ctx context.Context, token string) error`
  - `func RandomToken(n int) (string, error)`
  - `func FromRequest(r *http.Request) string`

- [ ] **Step 1: Add the oauth2 dependency**

Run:
```bash
go get golang.org/x/oauth2@v0.24.0
go mod tidy
```
Expected: `go.mod` gains a `require golang.org/x/oauth2 v0.24.0` line (or whatever `go mod tidy` resolves — pin to the latest v0.24.x at implementation time).

- [ ] **Step 2: Write the failing tests**

`internal/auth/service_test.go`:

```go
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
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/auth/... -v`
Expected: build failure (`Service undefined`, `ErrNoSession undefined`, etc.)

- [ ] **Step 4: Implement `internal/auth/service.go`**

```go
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
```

Delete `internal/auth/doc.go` (its content is now the package comment above).

- [ ] **Step 5: Run tests to verify they pass**

Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/auth/... -v`
Expected: PASS (all tests)

- [ ] **Step 6: Run the whole suite**

Run: `go test ./... -race` and `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./... -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/auth/service.go internal/auth/service_test.go go.mod go.sum
git rm internal/auth/doc.go
git commit -m "Add Google OAuth login and session handling"
```

---

## Task 6: `cmd/server` — auto-run migrations and wire auth

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `auth.NewService` (Task 5), `db.NewGameStore` (Task 4) — actually wired per-request in Tasks 7/8, not here; this task only constructs the shared `*auth.Service` and passes it (plus `*sql.DB`) into both handlers, whose constructors change in Tasks 7/8.
- Produces: server fails fast if migrations can't be applied, same as it already does for a bad `DATABASE_URL`.

- [ ] **Step 1: Implement — replace `cmd/server/main.go`**

```go
package main

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/pustserg/sudoku/internal/api"
	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/config"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
	"github.com/pustserg/sudoku/internal/web"
)

func main() {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	sqlDB, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" || cfg.GoogleRedirectURL == "" {
		log.Fatal("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_REDIRECT_URL must all be set")
	}
	authSvc := auth.NewService(sqlDB, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	anonStore := game.NewStore()
	puzzleLookup := game.PuzzleLookup(func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		return db.RandomPuzzle(ctx, sqlDB, d)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler)
	api.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB).Register(mux)
	web.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB).Register(mux)

	addr := ":" + cfg.Port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// runMigrations applies every pending migration in migrations/ to
// databaseURL, so a fresh checkout only needs `go run ./cmd/server` —
// no separate `go run ./cmd/migrate up` step. A real failure (bad SQL,
// dirty migration state) still fails startup fast, same as an invalid
// DATABASE_URL.
func runMigrations(databaseURL string) error {
	m, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
```

This won't compile yet — `api.NewHandler` and `web.NewHandler` still have their old 2-argument signatures. That's expected; Tasks 7 and 8 update them next. Don't run `go build ./cmd/server` as a pass/fail gate for this task; just confirm the diff matches the above.

- [ ] **Step 2: Commit**

```bash
git add cmd/server/main.go
git commit -m "Auto-run migrations on server startup and wire auth service"
```

(A red `go build ./...` between this commit and the end of Task 8 is expected and fine on this branch — each intermediate commit here is a checkpoint, not a release.)

---

## Task 7: `internal/api` — auth endpoints and per-request store selection

**Files:**
- Modify: `internal/api/handler.go`
- Modify: `internal/api/handler_test.go`

**Interfaces:**
- Consumes: `auth.Service`, `auth.FromRequest`, `auth.ErrNoSession` (Task 5); `db.NewGameStore` (Task 4).
- Produces: `func NewHandler(anonStore *game.Store, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler`; routes `POST /api/auth/google/callback`, `POST /api/auth/logout`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/api/handler_test.go` (adjust the existing `testHandler` helper and imports):

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
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

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
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
```

`loginTestUser` needs a way to hash the token the same way `internal/auth` does, without duplicating the SHA-256 logic in a second package. Add one small exported test helper to `internal/auth/service.go` (Task 5's file) rather than re-deriving the hash here:

```go
// TokenHashForTest exposes tokenHash to other packages' tests only. Not
// for production use — production code never needs a raw token's hash
// outside this package.
func TokenHashForTest(token string) []byte {
	return tokenHash(token)
}
```

(Go back and add that one function to `internal/auth/service.go` now, and re-run `go test ./internal/auth/...` to confirm it still passes — it's an additive, untested-by-design helper, so no new test is needed for it.)

Now the actual new tests, appended to `internal/api/handler_test.go`:

```go
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

	var count int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM games").Scan(&count); err != nil {
		t.Fatalf("query games count: %v", err)
	}
	if count == 0 {
		t.Error("no row was written to the games table for an authenticated create")
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(context.Background(), "DELETE FROM games")
	})
}

func TestCreateGameAnonymousDoesNotTouchDB(t *testing.T) {
	h, _, sqlDB := testHandlerWithAuth(t)

	req := httptest.NewRequest("POST", "/api/games", bytes.NewBufferString(`{"difficulty":"easy"}`))
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var count int
	if err := sqlDB.QueryRowContext(context.Background(), "SELECT count(*) FROM games").Scan(&count); err != nil {
		t.Fatalf("query games count: %v", err)
	}
	if count != 0 {
		t.Errorf("anonymous create wrote %d rows to games, want 0", count)
		sqlDB.ExecContext(context.Background(), "DELETE FROM games")
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
```

Add `"database/sql"` to the import block (used by `testHandlerWithAuth`'s `sqlDB *sql.DB` return type).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go build ./...` first — expect a failure because `NewHandler` still takes 2 args. Fix the signature in Step 3, then run:
Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/api/... -v`
Expected: FAIL on the new tests specifically (404s / route not found) once the build succeeds.

- [ ] **Step 3: Implement — update `internal/api/handler.go`**

Change the struct, constructor, `Register`, and add a `storeFor` helper and the two new endpoints:

```go
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// Handler serves the JSON REST API for creating and playing games, plus
// Google OAuth login/logout for API (e.g. future mobile) clients.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
}

// NewHandler returns a Handler. anonStore backs anonymous play; puzzles
// pulls new puzzles; auth and sqlDB back Google login and per-user game
// persistence. auth and sqlDB may be nil in tests that don't exercise
// the authenticated path — storeFor then always falls back to
// anonStore, and the auth endpoints are not expected to be called.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/games", h.createGame)
	mux.HandleFunc("GET /api/games/{id}", h.getGame)
	mux.HandleFunc("POST /api/games/{id}/moves", h.submitMove)
	mux.HandleFunc("POST /api/auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /api/auth/logout", h.logout)
}

// storeFor returns the GameStore to use for r: a user-scoped
// db.GameStore if r carries a valid session, otherwise h.anonStore.
func (h *Handler) storeFor(r *http.Request) game.GameStore {
	if h.auth == nil {
		return h.anonStore
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		return h.anonStore
	}
	return db.NewGameStore(h.sqlDB, user.ID)
}
```

Update `createGame`, `getGame`, and `submitMove` to call `h.storeFor(r)` instead of `h.store` (everything else about those three functions stays as in Task 3's Step 5):

```go
func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d, ok := game.ParseDifficulty(req.Difficulty)
	if !ok {
		writeError(w, http.StatusBadRequest, "difficulty must be one of easy, medium, hard, expert")
		return
	}

	p, err := h.puzzles(r.Context(), d)
	if err != nil {
		log.Printf("api: puzzle lookup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not find a puzzle right now")
		return
	}

	g, err := h.storeFor(r).Create(r.Context(), p)
	if err != nil {
		log.Printf("api: create game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create game")
		return
	}
	writeJSON(w, http.StatusCreated, toGameState(g))
}

func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := h.storeFor(r).Get(r.Context(), id)
	if errors.Is(err, game.ErrNotFound) {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	if err != nil {
		log.Printf("api: get game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load game")
		return
	}
	writeJSON(w, http.StatusOK, toGameState(g))
}
```

And in `submitMove`, change `h.store.ApplyMove(...)` to `h.storeFor(r).ApplyMove(...)`.

Add the two new handlers at the end of the file, before `writeJSON`:

```go
type googleCallbackRequest struct {
	Code string `json:"code"`
}

type googleCallbackResponse struct {
	Token string `json:"token"`
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	var req googleCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, err := h.auth.HandleCallback(r.Context(), req.Code)
	if err != nil {
		log.Printf("api: google callback failed: %v", err)
		writeError(w, http.StatusUnauthorized, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, googleCallbackResponse{Token: token})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), auth.FromRequest(r)); err != nil {
		log.Printf("api: logout failed: %v", err)
		writeError(w, http.StatusInternalServerError, "logout failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/api/... -v`
Expected: PASS (all tests, including the pre-existing ones — `testHandler`'s callers now pass `nil, nil` for `authSvc, sqlDB`, which `storeFor` handles by always falling back to `anonStore`)

Also run: `go vet ./...` (catches the case where `h.auth` is `nil` and `googleCallback`/`logout` would be called — not exercised by `testHandler`-based tests, which is correct: those tests never hit the auth routes).

- [ ] **Step 5: Commit**

```bash
git add internal/api/handler.go internal/api/handler_test.go internal/auth/service.go
git commit -m "Add Google OAuth endpoints and per-request store selection to the API"
```

---

## Task 8: `internal/web` — login/logout UI and per-request store selection

**Files:**
- Modify: `internal/web/web.go`
- Modify: `internal/web/templates/home.html`

**Interfaces:**
- Consumes: same as Task 7 (`auth.Service`, `auth.FromRequest`, `db.NewGameStore`).
- Produces: `func NewHandler(anonStore *game.Store, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler`; routes `GET /auth/google/login`, `GET /auth/google/callback`, `POST /logout`.

Note on scope: the spec's "Continue vs New game" UI choice is satisfied for free by `db.GameStore.Create`'s resume behavior (Task 4) — clicking a difficulty button always lands you on your in-progress game for that difficulty if one exists, otherwise starts a fresh one. This task adds a login/logout affordance to the home page but does not add a separate "Continue" button, which would need extra plumbing (querying active-game state before the button is even clicked) for no behavioral difference.

- [ ] **Step 1: Implement — update `internal/web/web.go`**

Change imports, struct, constructor, `Register`, `home`, and add `storeFor` plus the three new handlers:

```go
package web

import (
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// stateCookieName holds the anti-CSRF state value between the redirect
// to Google and the callback. Short-lived and httpOnly; never read by
// anything but googleCallback.
const stateCookieName = "oauth_state"

// sessionCookieMaxAge matches auth.sessionLifetime (30 days), so the
// browser doesn't keep sending a cookie the server has already expired.
const sessionCookieMaxAge = 30 * 24 * 60 * 60

// Handler serves the htmx web UI, including Google OAuth login/logout.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
}

// NewHandler returns a Handler. See api.NewHandler's doc comment for
// the meaning of each parameter — the two packages mirror each other.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("POST /play", h.createGame)
	mux.HandleFunc("GET /play/{id}", h.showBoard)
	mux.HandleFunc("POST /play/{id}/moves", h.submitMove)
	mux.HandleFunc("GET /auth/google/login", h.googleLogin)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /logout", h.logout)
}

// storeFor mirrors api.Handler.storeFor: a user-scoped db.GameStore if r
// carries a valid session, otherwise h.anonStore.
func (h *Handler) storeFor(r *http.Request) game.GameStore {
	if h.auth == nil {
		return h.anonStore
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		return h.anonStore
	}
	return db.NewGameStore(h.sqlDB, user.ID)
}

// currentUserEmail returns the signed-in user's email for r, or "" if
// anonymous — used only to decide what the home page's header shows.
func (h *Handler) currentUserEmail(r *http.Request) string {
	if h.auth == nil {
		return ""
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		return ""
	}
	return user.Email
}

type homeView struct {
	LoggedIn bool
	Email    string
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	email := h.currentUserEmail(r)
	view := homeView{LoggedIn: email != "", Email: email}
	if err := templates.ExecuteTemplate(w, "home", view); err != nil {
		log.Printf("web: render home: %v", err)
	}
}
```

Update `createGame`, `showBoard`, and `submitMove` to call `h.storeFor(r)` instead of `h.store` (same substitution as Task 7 — logic otherwise unchanged from Task 3's Step 7).

Add the three new handlers after `submitMove`:

```go
func (h *Handler) googleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := auth.RandomToken(16)
	if err != nil {
		log.Printf("web: generate oauth state: %v", err)
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   600, // 10 minutes: long enough for a login round trip, short-lived by design
	})
	http.Redirect(w, r, h.auth.LoginURL(state), http.StatusFound)
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: "/", MaxAge: -1})

	token, err := h.auth.HandleCallback(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("web: oauth callback: %v", err)
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   sessionCookieMaxAge,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), auth.FromRequest(r)); err != nil {
		log.Printf("web: logout: %v", err)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusFound)
}
```

- [ ] **Step 2: Update `internal/web/templates/home.html`**

Add a login/logout line under the tagline (inside the existing `.card` div, right after `<p class="tagline">Pick a difficulty to start</p>`):

```html
  {{if .LoggedIn}}
  <p class="account">Signed in as {{.Email}} · <a href="#" onclick="document.getElementById('logout-form').submit(); return false;">Log out</a></p>
  <form id="logout-form" method="POST" action="/logout" style="display:none;"></form>
  {{else}}
  <p class="account"><a href="/auth/google/login">Sign in with Google</a></p>
  {{end}}
```

And add matching styles inside the existing `<style>` block (next to the `p.tagline` rule):

```css
  p.account { color: #7188b8; margin: 0 0 20px; font-size: 0.9rem; }
  p.account a { color: #3b5db8; text-decoration: none; font-weight: 600; }
  p.account a:hover { text-decoration: underline; }
```

- [ ] **Step 3: Fix `internal/web`'s existing tests for the new constructor signature**

`internal/web` doesn't currently have a `_test.go` file (confirm with `ls internal/web/*_test.go`); if that's still true, there's nothing to fix here. If a test file has been added since this plan was written, update its `NewHandler` calls the same way Task 7 Step 4 did for `internal/api`.

- [ ] **Step 4: Run the whole suite**

Run: `go build ./...` (now everything compiles, including `cmd/server`)
Run: `go test ./... -race`
Run: `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./... -v`
Expected: PASS across the board.

- [ ] **Step 5: Manual verification (per AGENTS.md — no automated browser suite for v1)**

With a migrated local Postgres and real `.env` Google credentials:

```bash
make run
```

Then in a browser:
1. Visit `http://localhost:8080/` — see "Sign in with Google" link, difficulty buttons work anonymously as before (games not persisted — restart the server mid-game and confirm it's gone).
2. Click "Sign in with Google", complete the real consent flow, land back on `/` showing "Signed in as <email>".
3. Click "Easy" — play a few moves, note the game URL's id.
4. Restart the server (`Ctrl+C`, `make run` again) and visit the same game URL directly — the game and its moves are still there (proves Postgres persistence survives a restart).
5. From `/`, click "Easy" again — confirm it returns you to the same in-progress game (id in the URL matches step 3's), not a new one.
6. Solve or intentionally fail that game, then click "Easy" again — confirm this time it's a new game (different id).
7. Click "Log out" — back to "Sign in with Google", and games created while logged in are no longer reachable anonymously.

Note in the eventual PR description that this flow was checked manually.

- [ ] **Step 6: Commit**

```bash
git add internal/web/web.go internal/web/templates/home.html
git commit -m "Add Google login/logout UI and per-request store selection to the web UI"
```

---

## Task 9: Update docs to match the shipped behavior

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `ROADMAP.md`

**Interfaces:** None — documentation only.

- [ ] **Step 1: Update `README.md`'s "Getting started"**

Remove the `go run ./cmd/migrate up` step (migrations now run automatically on server start, per Task 6) and add a step noting the three `GOOGLE_*` env vars are required. Keep `cmd/migrate` documented as still available for `down` or manual use.

- [ ] **Step 2: Update `ROADMAP.md`**

Mark Phase 3's checklist items as done (or add a short "Status: shipped" note under the `## Phase 3` heading) so the roadmap reflects reality for whoever reads it next.

- [ ] **Step 3: Update `AGENTS.md`**

Under "Architecture rules", update the Auth bullet to mention this phase's actual scope (Google only for now; GitHub still deferred) and add a bullet noting migrations now run automatically at server startup (`cmd/migrate` remains for `down`/manual use).

- [ ] **Step 4: Commit**

```bash
git add README.md AGENTS.md ROADMAP.md
git commit -m "Update docs for Phase 3 (accounts, auto-migrations)"
```

---

## After all tasks

Run the full check once more (`make check` or `gofmt -l . && go vet ./... && go test ./...`, plus `SUDOKU_TEST_DATABASE_URL="$DATABASE_URL" go test ./... -v` for the Postgres-backed tests), then follow `superpowers:finishing-a-development-branch` to decide how this branch gets integrated — per `AGENTS.md`, that's a decision for the user, not something to do automatically.
