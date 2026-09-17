# Play loop design (`internal/game`, `internal/db.RandomPuzzle`, `internal/api`, `internal/web`)

Date: 2026-09-14
Status: Approved for implementation
Part of: Roadmap Phase 2, sub-project 3 of 3 (engine → puzzle pool → **play loop**)

## Purpose

Let a player create and play a Sudoku game end-to-end: pick a difficulty,
get a puzzle pulled from the pregenerated pool, fill in cells, and see
when it's solved — via both a JSON REST API (for the current htmx UI and
a future mobile client) and a server-rendered htmx web UI. No accounts yet
(Phase 3). Game state itself is in-memory for this phase; only the puzzle
pool read needs Postgres, per `ROADMAP.md`.

Per `AGENTS.md`: the REST API is the one source of truth for game state
changes, and htmx handlers must call into the same internal service layer
the REST API uses, not a second divergent code path. This design puts
that shared layer in a new `internal/game` package, imported by both
`internal/api` and `internal/web`, rather than inside `internal/api`
itself — keeping HTTP-transport code separate from the actual game logic
so each package has one clear responsibility.

## Components

### 1. `internal/game` — core service layer

```go
package game

import "github.com/pustserg/sudoku/internal/sudoku"

// Game is one in-progress (or completed) puzzle session.
type Game struct {
	ID         string
	Givens     sudoku.Grid // original clues; never mutated after creation
	Current    sudoku.Grid // the player's working grid
	Solution   sudoku.Grid
	Difficulty sudoku.Difficulty
}

// Solved reports whether Current matches Solution exactly.
func (g *Game) Solved() bool

// Store holds all in-progress games in memory, safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	games map[string]*Game
}

func NewStore() *Store

// Create starts a new Game from a freshly pulled puzzle and returns it.
func (s *Store) Create(p sudoku.Puzzle) *Game

// Get returns the game with the given ID, or ok=false if it doesn't exist.
func (s *Store) Get(id string) (g *Game, ok bool)

// ErrOutOfRange: row/col not in 0-8, or value not in 0-9.
// ErrGivenCell: the target cell is one of the puzzle's original clues.
// ErrNotFound: id doesn't match any game in the store.
var (
	ErrOutOfRange error
	ErrGivenCell  error
	ErrNotFound   error
)

// ApplyMove sets Current[row][col] = value (0 clears the cell) on the
// game with the given id, and returns the updated Game. value must be
// 0-9; row and col must be 0-8. A cell that is non-zero in Givens cannot
// be changed (ErrGivenCell) — clues are fixed for the life of the game.
func (s *Store) ApplyMove(id string, row, col, value int) (*Game, error)
```

- **ID generation**: `crypto/rand`-sourced, hex-encoded (16 random bytes →
  32 hex characters) — opaque, unguessable enough for anonymous play
  where the ID itself is the only "access control," unlinked to any
  account (there are none yet).
- **Concurrency**: `Store` is a singleton, constructed once in
  `cmd/server/main.go` and passed to both `internal/api` and
  `internal/web`'s constructors. A single `sync.Mutex` guards the map;
  contention is a non-issue at this scale (in-memory, single-process, no
  long-held locks).
- **Validation boundary**: `ApplyMove` only rejects structurally invalid
  input (out-of-range indices/values) and attempts to overwrite a given
  clue. It does **not** check Sudoku legality (no row/col/box conflict
  detection) — a player can place a digit that creates a conflict, and
  `Solved()` will simply report `false` until the grid matches the
  solution exactly. This matches v1 scope; mistake-detection/hints are
  explicitly a `ROADMAP.md` Phase 5 polish item, not this phase.
- `Game`'s `Givens`/`Solution` are never mutated after `Create`; only
  `Current` changes via `ApplyMove`.

**Testing**: table-driven unit tests for `Store.Create`/`Get`/`ApplyMove`
(including each error case) and `Game.Solved`, no I/O involved.

### 2. `internal/db` — puzzle selection

```go
// RandomPuzzle returns one randomly-selected puzzle of difficulty d from
// the puzzles table. Returns an error wrapping sql.ErrNoRows (with a
// hint to run cmd/genpuzzles) if no puzzle of that difficulty exists yet.
func RandomPuzzle(ctx context.Context, sqlDB *sql.DB, d sudoku.Difficulty) (sudoku.Puzzle, error)
```

Query: `SELECT givens, solution, difficulty FROM puzzles WHERE difficulty
= $1 ORDER BY random() LIMIT 1`. `ORDER BY random()` sorts the whole
matching set — acceptable at this pool's scale (tens of thousands of rows
per tier per the roadmap's 200k-300k target) but a known scaling
limitation, not something this phase needs to optimize (e.g. via
`TABLESAMPLE` or a pre-shuffled offset scheme) — revisit only if it
becomes a measured problem.

**Testing**: one integration test gated on `SUDOKU_TEST_DATABASE_URL`
(same pattern as `internal/db`'s existing tests), inserting a few known
puzzles via `InsertPuzzles` and confirming `RandomPuzzle` returns one of
the expected difficulty and that repeated calls can return different rows
(not proof of randomness, just that it isn't hardcoded to always return
the first inserted row).

### 3. REST API (`internal/api`)

Handlers are constructed with a `*game.Store` and a puzzle-lookup
function value, not a raw `*sql.DB` — this is the seam that lets handler
tests substitute a fake without a live Postgres:

```go
// PuzzleLookup matches db.RandomPuzzle's signature; production code
// passes db.RandomPuzzle itself, tests pass a stub.
type PuzzleLookup func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error)

type Handler struct {
	store   *game.Store
	puzzles PuzzleLookup
}

func NewHandler(store *game.Store, puzzles PuzzleLookup) *Handler
```

`cmd/server/main.go` constructs the real one as `api.NewHandler(store,
func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
return db.RandomPuzzle(ctx, sqlDB, d) })`. `internal/web` takes the same
two dependencies, for the same reason and the same test seam.

- **`POST /api/games`** — body `{"difficulty":"easy"|"medium"|"hard"|"expert"}`.
  Parses the difficulty string (a small map in `internal/api`, not added
  to `internal/sudoku`, since it's a wire-format/DTO concern), calls
  `db.RandomPuzzle`, then `store.Create`. Returns `201` with the game
  state JSON. `400` for an unparseable/missing difficulty string. `500`
  (with a generic body, details logged server-side) if `RandomPuzzle`
  fails (e.g. pool exhausted for that tier, or a DB error).
- **`GET /api/games/{id}`** — `200` with state JSON, `404` if unknown.
- **`POST /api/games/{id}/moves`** — body `{"row":int,"col":int,"value":int}`.
  Calls `store.ApplyMove`. `200` with updated state JSON on success.
  `404` if the game ID doesn't exist (`game.ErrNotFound`). `400` for
  `game.ErrOutOfRange` or `game.ErrGivenCell` (with a short JSON error
  body naming which one).

State JSON shape (used by both the create and fetch/move responses):

```json
{
  "id": "3f9a2b...",
  "givens": [[5,3,0,0,7,0,0,0,0], "...8 more rows..."],
  "current": [[5,3,0,0,7,0,0,0,0], "...8 more rows..."],
  "difficulty": "easy",
  "solved": false
}
```

**Testing**: `net/http/httptest`-based handler tests per `AGENTS.md`,
covering: successful create for each difficulty string, invalid
difficulty string, fetch of an existing and a nonexistent game, a
successful move, a move on a given cell, an out-of-range move, and a move
against a nonexistent game. These tests use an in-memory `*game.Store`
directly; the `db.RandomPuzzle` dependency is exercised through a small
seam (see Testing note in the plan) so handler tests don't need a live
Postgres.

### 4. htmx web UI (`internal/web`)

Routes (registered in `cmd/server/main.go` alongside `/healthz` and the
`/api/...` routes):

- **`GET /`** — home page: one form per difficulty (four buttons), each
  `POST`ing to `/play`.
- **`POST /play`** — reads the submitted difficulty, creates a game via
  the exact same `game.Store`/`db.RandomPuzzle` call the REST API uses,
  then `http.Redirect`s (`302`) to `GET /play/{id}`.
- **`GET /play/{id}`** — renders the full board page: `404` page if the
  ID doesn't exist. Given cells render as fixed text; empty cells render
  as `<select>` elements (options 0-9, 0 = blank) inside a form with
  `hx-post="/play/{id}/moves" hx-trigger="change" hx-target="#board"
  hx-swap="outerHTML"` — each cell change auto-submits and swaps in the
  freshly rendered board.
- **`POST /play/{id}/moves`** — form fields `row`, `col`, `value`. Calls
  `store.ApplyMove`, then re-renders only the `#board` fragment (an
  `html/template` block shared by both the full-page and fragment
  responses, so the board markup is defined once) — including a
  "Solved!" banner when `Game.Solved()` is true.

Templates live in `internal/web/templates/`: a base layout, `home.html`,
and `board.html` (which defines the reusable `{{block "board" .}}`
fragment the full-page template also includes).

**Testing**: no automated browser suite for v1, per `AGENTS.md` — verified
manually by running the server and playing a puzzle through in a real
browser before this sub-project is considered done, noted in the
commit/PR description as checked manually.

## Data flow

```
Client (curl/mobile, or htmx page)
  → POST /api/games {difficulty}          → db.RandomPuzzle → game.Store.Create → 201 {state}
  → GET  /api/games/{id}                  → game.Store.Get                      → 200 {state} | 404
  → POST /api/games/{id}/moves {row,col,value} → game.Store.ApplyMove           → 200 {state} | 400 | 404

Browser (htmx)
  → GET  /                                 → static difficulty picker
  → POST /play {difficulty}                → (same Create call as REST) → 302 /play/{id}
  → GET  /play/{id}                        → full board page
  → POST /play/{id}/moves {row,col,value} → (same ApplyMove call as REST) → #board fragment
```

Both surfaces call the identical `game.Store` methods — no divergent game
logic between REST and htmx, per `AGENTS.md`.

## Error handling

- `internal/game`'s three sentinel errors (`ErrOutOfRange`, `ErrGivenCell`,
  `ErrNotFound`) let both `internal/api` and `internal/web` map the same
  failure to their respective response formats (JSON error body vs. an
  htmx-friendly error fragment/status) without duplicating validation
  logic.
- `internal/api` never leaks internal error text (e.g. a raw SQL error)
  to the client; it logs the detailed error server-side and returns a
  generic message.
- `internal/web`'s error cases (unknown game ID, invalid difficulty form
  value) render a simple error page/fragment rather than a JSON body.

## Out of scope for this sub-project

- Accounts, sessions, save/resume — Phase 3.
- Realtime sync across tabs/devices — Phase 4.
- Mistake detection, hints, difficulty-selection UX polish — Phase 5.
- Optimizing `RandomPuzzle`'s `ORDER BY random()` — revisit only if
  measured as a real bottleneck.
