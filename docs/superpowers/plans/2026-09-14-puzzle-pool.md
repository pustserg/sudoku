# Puzzle Pool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a persistent, pregenerated puzzle pool: the `puzzles` table migration, `internal/db` (connection + grid/string conversion + batched insert), `cmd/migrate` (schema application), and `cmd/genpuzzles` (concurrent bulk generator).

**Architecture:** A `puzzles` table storing givens/solution as flattened 81-character digit strings plus a difficulty tier. `internal/db` is the only code that talks to Postgres, via `database/sql` with the `pgx/v5` stdlib driver — no ORM. `cmd/migrate` applies `migrations/` via the `golang-migrate` library. `cmd/genpuzzles` runs a worker pool that calls `internal/sudoku.Generate` concurrently and funnels results through one writer goroutine into batched `internal/db.InsertPuzzles` calls.

**Tech Stack:** Go 1.23.1, PostgreSQL (via `docker-compose.yml`'s `postgres` service), `github.com/jackc/pgx/v5` (stdlib driver), `github.com/golang-migrate/migrate/v4` (with its `postgres` and `file` drivers).

**Spec:** `docs/superpowers/specs/2026-09-14-puzzle-pool-design.md`

## Global Constraints

- `internal/db` uses `database/sql` with explicit queries only — no ORM, no repository abstraction hiding SQL (`AGENTS.md`).
- Migrations use `golang-migrate`; never hand-edit an already-applied migration file (`AGENTS.md`).
- `cmd/genpuzzles` reuses `internal/sudoku` for generation; it must never generate puzzles anywhere but this offline path (`AGENTS.md`).
- `givens`/`solution` are stored as 81-character strings, `'0'`-`'9'` (`'0'` = empty), row-major — identical cell order to `sudoku.Grid` (spec).
- `difficulty` stores `int(sudoku.Difficulty)` directly: 0=Easy, 1=Medium, 2=Hard, 3=Expert (spec).
- `puzzles.givens` has a `UNIQUE` constraint; `InsertPuzzles` must treat a conflict as a silent skip, not an error (spec).
- Follow `gofmt` formatting; every unit-testable piece of logic gets a table-driven test (`AGENTS.md`).
- Work happens on branch `phase-2-sudoku-engine` (already checked out), not `main`.

---

## File Structure

- `migrations/0001_create_puzzles.up.sql`, `migrations/0001_create_puzzles.down.sql` — the schema.
- `cmd/migrate/main.go` — applies migrations via the `golang-migrate` library; `up`/`down` subcommand.
- `internal/db/convert.go` — pure `gridToString`/`stringToGrid` conversion between `sudoku.Grid` and the stored 81-character format.
- `internal/db/convert_test.go` — table-driven tests for the above, no DB needed.
- `internal/db/db.go` — `Open(databaseURL string) (*sql.DB, error)`: pgx-backed connection pool with sane defaults and a startup ping.
- `internal/db/puzzles.go` — `InsertPuzzles` and the pure `chunkPuzzles` helper it uses internally.
- `internal/db/puzzles_test.go` — unit tests for `chunkPuzzles` (no DB) plus one integration test for `Open`/`InsertPuzzles`, gated on the `SUDOKU_TEST_DATABASE_URL` environment variable.
- `cmd/genpuzzles/jobs.go` — pure `buildJobs` helper turning four counts into a difficulty job list.
- `cmd/genpuzzles/jobs_test.go` — table-driven test for `buildJobs`, no DB/goroutines needed.
- `cmd/genpuzzles/main.go` — CLI flags, worker pool, writer goroutine, wiring (replaces the current stub).

`internal/db/doc.go` and `internal/sudoku` (all of it) already exist and need no changes.

---

### Task 1: Puzzles table migration

**Files:**
- Create: `migrations/0001_create_puzzles.up.sql`
- Create: `migrations/0001_create_puzzles.down.sql`

**Interfaces:**
- Produces: the `puzzles` table (`id BIGSERIAL PRIMARY KEY`, `givens CHAR(81) NOT NULL`, `solution CHAR(81) NOT NULL`, `difficulty SMALLINT NOT NULL`, `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`, `UNIQUE (givens)`) and `idx_puzzles_difficulty`, consumed by every later task in this plan.

This task has no Go code, so its "test" is applying the SQL directly against a real Postgres instance and confirming the schema and the down-migration both work, since `cmd/migrate` (which will apply these the normal way) doesn't exist until Task 2.

- [ ] **Step 1: Write the migration files**

Create `migrations/0001_create_puzzles.up.sql`:

```sql
CREATE TABLE puzzles (
    id         BIGSERIAL PRIMARY KEY,
    givens     CHAR(81) NOT NULL,
    solution   CHAR(81) NOT NULL,
    difficulty SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (givens)
);
CREATE INDEX idx_puzzles_difficulty ON puzzles (difficulty);
```

Create `migrations/0001_create_puzzles.down.sql`:

```sql
DROP TABLE puzzles;
```

- [ ] **Step 2: Start a real Postgres to verify against**

Run: `docker compose up -d postgres`
Then wait for it to report healthy:
Run: `docker compose ps postgres`
Expected: `STATUS` column shows `healthy` (poll every couple of seconds if it still says `starting`).

- [ ] **Step 3: Apply the up-migration directly and inspect the schema**

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -f migrations/0001_create_puzzles.up.sql`
Expected: `CREATE TABLE` then `CREATE INDEX`, no errors.

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c '\d puzzles'`
Expected: output listing all five columns with the types above, a primary key on `id`, and a unique constraint on `givens`.

- [ ] **Step 4: Apply the down-migration and confirm it's clean**

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -f migrations/0001_create_puzzles.down.sql`
Expected: `DROP TABLE`, no errors.

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c '\d puzzles'`
Expected: `Did not find any relation named "puzzles".` (confirms the down-migration actually removed it).

- [ ] **Step 5: Commit**

```bash
git add migrations/0001_create_puzzles.up.sql migrations/0001_create_puzzles.down.sql
git commit -m "Add puzzles table migration"
```

---

### Task 2: cmd/migrate

**Files:**
- Create: `cmd/migrate/main.go`
- Modify: `go.mod`, `go.sum` (via `go get`)

**Interfaces:**
- Consumes: `migrations/0001_create_puzzles.{up,down}.sql` (Task 1); `internal/config.Load() Config` (already exists, `Config.DatabaseURL string`).
- Produces: the `go run ./cmd/migrate up` / `go run ./cmd/migrate down` commands that every later task's DB-backed verification step relies on to prepare a real test database.

- [ ] **Step 1: Add the golang-migrate dependency**

Run: `go get github.com/golang-migrate/migrate/v4@latest`
Expected: `go.mod`/`go.sum` updated, no errors. (This pulls in the library's `postgres` database driver and `file` source driver as part of the same module — no separate `go get` needed for those subpackages.)

- [ ] **Step 2: Write cmd/migrate/main.go**

Create `cmd/migrate/main.go`:

```go
// Command migrate applies the migrations in migrations/ to the database
// named by DATABASE_URL, via the golang-migrate library.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/pustserg/sudoku/internal/config"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 || (args[0] != "up" && args[0] != "down") {
		log.Fatal("usage: migrate <up|down>")
	}
	direction := args[0]

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	m, err := migrate.New("file://migrations", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("create migrator: %v", err)
	}

	if direction == "up" {
		err = m.Up()
	} else {
		err = m.Down()
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("migrate %s: %v", direction, err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		log.Fatalf("read migration version: %v", err)
	}
	fmt.Printf("migration version: %d (dirty=%v)\n", version, dirty)
}
```

- [ ] **Step 3: Run go mod tidy**

Run: `go mod tidy`
Expected: no errors; `go.sum` gains entries for `golang-migrate` and its postgres/file driver dependencies (e.g. `lib/pq`, used internally by golang-migrate's postgres driver — this is separate from and does not replace this project's own use of `pgx` for application queries in Task 4).

- [ ] **Step 4: Verify it builds**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 5: Verify end-to-end against a real Postgres**

Make sure Postgres is up (from Task 1, or start it again if needed):
Run: `docker compose up -d postgres && docker compose ps postgres`
Expected: `healthy`.

Confirm the table doesn't already exist from Task 1's manual test (it shouldn't — Task 1 ended with the down-migration applied):
Run: `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' psql "$DATABASE_URL" -c '\d puzzles'`
Expected: `Did not find any relation named "puzzles".` If it does exist, run `psql "$DATABASE_URL" -f migrations/0001_create_puzzles.down.sql` first.

Run the migrator:
Run: `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate up`
Expected: `migration version: 1 (dirty=false)`, no errors.

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c '\d puzzles'`
Expected: the same five-column schema as Task 1's manual check.

Confirm idempotency — running `up` again on an already-migrated database must not error:
Run: `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate up`
Expected: `migration version: 1 (dirty=false)`, no errors (the `ErrNoChange` case is handled, not fatal).

Run the down-migration and confirm the table is removed:
Run: `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate down`
Expected: `migration version: 0 (dirty=false)`, no errors.

Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c '\d puzzles'`
Expected: `Did not find any relation named "puzzles".`

Leave the database migrated back **up** for the next task:
Run: `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate up`
Expected: `migration version: 1 (dirty=false)`.

- [ ] **Step 6: Commit**

```bash
git add cmd/migrate/main.go go.mod go.sum
git commit -m "Add cmd/migrate to apply migrations via golang-migrate"
```

---

### Task 3: internal/db grid/string conversion

**Files:**
- Create: `internal/db/convert.go`
- Create: `internal/db/convert_test.go`

**Interfaces:**
- Consumes: `sudoku.Grid` (`internal/sudoku`, already built).
- Produces: `func gridToString(g sudoku.Grid) string` and `func stringToGrid(s string) (sudoku.Grid, error)` (both unexported, package `db`), consumed by Task 4's `InsertPuzzles`/integration test.

- [ ] **Step 1: Write the failing tests**

Create `internal/db/convert_test.go`:

```go
package db

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// validGivens is the standard Wikipedia example puzzle's givens, flattened
// to this package's storage format ('0' for empty).
const validGivens = "530070000600195000098000060800060003400803001700020006060000280000419005000080079"

func TestGridToString(t *testing.T) {
	var g sudoku.Grid
	g[0][0] = 5
	g[0][1] = 3
	g[8][8] = 9

	got := gridToString(g)
	if len(got) != 81 {
		t.Fatalf("len(gridToString(g)) = %d, want 81", len(got))
	}
	if got[0] != '5' || got[1] != '3' {
		t.Errorf("gridToString(g)[0:2] = %q, want \"53\"", got[0:2])
	}
	if got[80] != '9' {
		t.Errorf("gridToString(g)[80] = %q, want '9'", got[80])
	}
	if got[2] != '0' {
		t.Errorf("gridToString(g)[2] = %q, want '0' for an empty cell", got[2])
	}
}

func TestStringToGrid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "round-trips a grid with mixed empty and filled cells",
			input: validGivens,
		},
		{
			name:    "wrong length is an error",
			input:   "123",
			wantErr: true,
		},
		{
			name:    "invalid character is an error",
			input:   validGivens[:80] + "x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := stringToGrid(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("stringToGrid(%q) = nil error, want an error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("stringToGrid(%q) unexpected error: %v", tt.input, err)
			}
			roundTripped := gridToString(g)
			if roundTripped != tt.input {
				t.Errorf("round-trip mismatch: got %q, want %q", roundTripped, tt.input)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/db/... -v`
Expected: build failure — `gridToString`/`stringToGrid` are undefined (and package `db` may not build at all yet — that's expected).

- [ ] **Step 3: Implement convert.go**

Create `internal/db/convert.go`:

```go
// Package db contains PostgreSQL access via database/sql, no ORM.
package db

import (
	"fmt"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// gridToString flattens g into an 81-character string, row-major, '0'
// for empty cells and '1'-'9' for filled ones — the layout stored in the
// puzzles table's givens/solution columns.
func gridToString(g sudoku.Grid) string {
	b := make([]byte, 0, 81)
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			b = append(b, byte('0'+g[r][c]))
		}
	}
	return string(b)
}

// stringToGrid parses an 81-character digit string (as produced by
// gridToString) back into a Grid. It returns an error if s is not exactly
// 81 characters, each '0'-'9'.
func stringToGrid(s string) (sudoku.Grid, error) {
	var g sudoku.Grid
	if len(s) != 81 {
		return g, fmt.Errorf("stringToGrid: want 81 characters, got %d", len(s))
	}
	for i := 0; i < 81; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return sudoku.Grid{}, fmt.Errorf("stringToGrid: invalid character %q at position %d", c, i)
		}
		g[i/9][i%9] = int(c - '0')
	}
	return g, nil
}
```

Note: this replaces the existing one-line `internal/db/doc.go` package comment — fold that same comment into this file's package declaration (as shown above) and delete `internal/db/doc.go`, since a package comment must attach to some file and this is now the natural place for it.

- [ ] **Step 4: Delete the now-redundant doc.go**

```bash
git rm internal/db/doc.go
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/db/... -v`
Expected: PASS for `TestGridToString` and all three `TestStringToGrid` subtests.

- [ ] **Step 6: Commit**

```bash
git add internal/db/convert.go internal/db/convert_test.go
git commit -m "Add Grid/string conversion for puzzle storage"
```

---

### Task 4: internal/db connection and batched insert

**Files:**
- Create: `internal/db/db.go`
- Create: `internal/db/puzzles.go`
- Create: `internal/db/puzzles_test.go`
- Modify: `go.mod`, `go.sum` (via `go get`)

**Interfaces:**
- Consumes: `gridToString` (Task 3); `sudoku.Puzzle`, `sudoku.Grid`, `sudoku.Difficulty`, `sudoku.Easy` (`internal/sudoku`, already built); the `puzzles` table (Task 1, applied via `cmd/migrate` from Task 2).
- Produces: `func Open(databaseURL string) (*sql.DB, error)` and `func InsertPuzzles(ctx context.Context, sqlDB *sql.DB, puzzles []sudoku.Puzzle) error`, both consumed by Task 5's `cmd/genpuzzles`.

- [ ] **Step 1: Add the pgx dependency**

Run: `go get github.com/jackc/pgx/v5@latest`
Expected: `go.mod`/`go.sum` updated, no errors.

- [ ] **Step 2: Write the failing tests**

Create `internal/db/puzzles_test.go`:

```go
package db

import (
	"context"
	"os"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestChunkPuzzles(t *testing.T) {
	puzzles := make([]sudoku.Puzzle, 7)

	chunks := chunkPuzzles(puzzles, 3)

	if len(chunks) != 3 {
		t.Fatalf("len(chunks) = %d, want 3", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 || len(chunks[2]) != 1 {
		t.Errorf("chunk sizes = %d, %d, %d, want 3, 3, 1", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestChunkPuzzlesEmpty(t *testing.T) {
	chunks := chunkPuzzles(nil, 3)
	if len(chunks) != 0 {
		t.Errorf("len(chunks) = %d, want 0 for empty input", len(chunks))
	}
}

func TestChunkPuzzlesExactMultiple(t *testing.T) {
	puzzles := make([]sudoku.Puzzle, 6)
	chunks := chunkPuzzles(puzzles, 3)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 {
		t.Errorf("chunk sizes = %d, %d, want 3, 3", len(chunks[0]), len(chunks[1]))
	}
}

// testDatabaseURL returns the SUDOKU_TEST_DATABASE_URL environment
// variable, skipping the calling test if it's unset — these tests need a
// real, already-migrated Postgres database (see Task 4's verification
// steps for how to set one up) and don't run without one.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SUDOKU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SUDOKU_TEST_DATABASE_URL not set, skipping integration test")
	}
	return url
}

func TestOpenAndInsertPuzzles(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	defer sqlDB.Close()

	ctx := context.Background()
	var zeroGrid sudoku.Grid
	t.Cleanup(func() {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(zeroGrid)); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	// An all-zero grid is a fine, deterministic fixture here: it's not a
	// real puzzle, but InsertPuzzles doesn't validate puzzle content,
	// only stores what it's given, and Cleanup above guarantees no other
	// test run leaves one behind to collide with this one's uniqueness
	// constraint.
	puzzle := sudoku.Puzzle{
		Givens:     zeroGrid,
		Solution:   zeroGrid,
		Difficulty: sudoku.Easy,
	}
	if err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle}); err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(zeroGrid)).Scan(&count); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	// Inserting the same givens again must be a no-op (ON CONFLICT DO
	// NOTHING), not an error.
	if err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle}); err != nil {
		t.Fatalf("InsertPuzzles() duplicate insert error: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(zeroGrid)).Scan(&count); err != nil {
		t.Fatalf("query after duplicate insert: %v", err)
	}
	if count != 1 {
		t.Errorf("count after duplicate insert = %d, want 1 (ON CONFLICT DO NOTHING should prevent a second row)", count)
	}
}
```

- [ ] **Step 3: Run the non-DB tests to verify they fail**

Run: `go test ./internal/db/... -run TestChunkPuzzles -v`
Expected: build failure — `chunkPuzzles` is undefined.

- [ ] **Step 4: Implement db.go**

Create `internal/db/db.go`:

```go
package db

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open opens a connection pool to the Postgres database at databaseURL,
// via the pgx stdlib driver, and verifies connectivity with a ping
// before returning, so callers fail fast on bad configuration rather
// than on the first query.
func Open(databaseURL string) (*sql.DB, error) {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}
```

- [ ] **Step 5: Implement puzzles.go**

Create `internal/db/puzzles.go`:

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// insertChunkSize is the number of puzzles per multi-row INSERT
// statement within InsertPuzzles' transaction.
const insertChunkSize = 500

// InsertPuzzles inserts puzzles into the puzzles table inside a single
// transaction, batched into chunks of insertChunkSize rows per
// statement. A puzzle whose givens already exist in the table is
// silently skipped (ON CONFLICT DO NOTHING) rather than failing the
// whole call.
func InsertPuzzles(ctx context.Context, sqlDB *sql.DB, puzzles []sudoku.Puzzle) error {
	if len(puzzles) == 0 {
		return nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op once Commit succeeds

	for _, chunk := range chunkPuzzles(puzzles, insertChunkSize) {
		if err := insertChunk(ctx, tx, chunk); err != nil {
			return fmt.Errorf("insert chunk: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// chunkPuzzles splits puzzles into consecutive slices of at most size
// elements each.
func chunkPuzzles(puzzles []sudoku.Puzzle, size int) [][]sudoku.Puzzle {
	var chunks [][]sudoku.Puzzle
	for size < len(puzzles) {
		puzzles, chunks = puzzles[size:], append(chunks, puzzles[:size:size])
	}
	if len(puzzles) > 0 {
		chunks = append(chunks, puzzles)
	}
	return chunks
}

// insertChunk inserts one chunk of puzzles via a single multi-row
// INSERT statement.
func insertChunk(ctx context.Context, tx *sql.Tx, chunk []sudoku.Puzzle) error {
	var sb strings.Builder
	sb.WriteString("INSERT INTO puzzles (givens, solution, difficulty) VALUES ")
	args := make([]any, 0, len(chunk)*3)
	for i, p := range chunk {
		if i > 0 {
			sb.WriteString(", ")
		}
		n := i * 3
		fmt.Fprintf(&sb, "($%d, $%d, $%d)", n+1, n+2, n+3)
		args = append(args, gridToString(p.Givens), gridToString(p.Solution), int(p.Difficulty))
	}
	sb.WriteString(" ON CONFLICT (givens) DO NOTHING")

	_, err := tx.ExecContext(ctx, sb.String(), args...)
	return err
}
```

- [ ] **Step 6: Run go mod tidy**

Run: `go mod tidy`
Expected: no errors; `go.sum` gains `pgx/v5` and its dependencies.

- [ ] **Step 7: Run the non-DB tests to verify they pass**

Run: `go test ./internal/db/... -run TestChunkPuzzles -v`
Expected: PASS for `TestChunkPuzzles`, `TestChunkPuzzlesEmpty`, `TestChunkPuzzlesExactMultiple`.

- [ ] **Step 8: Run the integration test against a real Postgres**

Make sure Postgres is up and migrated (from Task 2 — it should already be at version 1; if you're picking this task up separately, run `docker compose up -d postgres` then `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate up` first):
Run: `docker compose ps postgres`
Expected: `healthy`.

Run the integration test:
Run: `SUDOKU_TEST_DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go test ./internal/db/... -run TestOpenAndInsertPuzzles -v`
Expected: PASS.

Run the full package test without the env var set, to confirm the integration test skips cleanly rather than failing in environments without a live Postgres:
Run: `go test ./internal/db/... -v`
Expected: `TestOpenAndInsertPuzzles` reports `SKIP` with the "not set" message; all other tests PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/db/db.go internal/db/puzzles.go internal/db/puzzles_test.go go.mod go.sum
git commit -m "Add Postgres connection and batched puzzle insert"
```

---

### Task 5: cmd/genpuzzles

**Files:**
- Create: `cmd/genpuzzles/jobs.go`
- Create: `cmd/genpuzzles/jobs_test.go`
- Modify: `cmd/genpuzzles/main.go` (replace the existing stub entirely)

**Interfaces:**
- Consumes: `sudoku.Generate`, `sudoku.Difficulty`, `sudoku.Easy`/`Medium`/`Hard`/`Expert`, `sudoku.Puzzle` (`internal/sudoku`); `db.Open`, `db.InsertPuzzles` (Task 4); `config.Load` (already exists).
- Produces: the `cmd/genpuzzles` binary — the final piece of this plan, nothing later in this plan depends on it.

- [ ] **Step 1: Write the failing test for buildJobs**

Create `cmd/genpuzzles/jobs_test.go`:

```go
package main

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestBuildJobs(t *testing.T) {
	jobs := buildJobs(2, 1, 0, 3)

	if len(jobs) != 6 {
		t.Fatalf("len(jobs) = %d, want 6", len(jobs))
	}

	counts := map[sudoku.Difficulty]int{}
	for _, d := range jobs {
		counts[d]++
	}
	want := map[sudoku.Difficulty]int{
		sudoku.Easy:   2,
		sudoku.Medium: 1,
		sudoku.Expert: 3,
	}
	for d, wantCount := range want {
		if counts[d] != wantCount {
			t.Errorf("counts[%v] = %d, want %d", d, counts[d], wantCount)
		}
	}
	if counts[sudoku.Hard] != 0 {
		t.Errorf("counts[Hard] = %d, want 0", counts[sudoku.Hard])
	}

	if jobs[0] != sudoku.Easy || jobs[1] != sudoku.Easy {
		t.Errorf("expected first two jobs to be Easy, got %v", jobs[:2])
	}
	if jobs[2] != sudoku.Medium {
		t.Errorf("expected third job to be Medium, got %v", jobs[2])
	}
	if jobs[3] != sudoku.Expert || jobs[4] != sudoku.Expert || jobs[5] != sudoku.Expert {
		t.Errorf("expected last three jobs to be Expert, got %v", jobs[3:])
	}
}

func TestBuildJobsAllZero(t *testing.T) {
	jobs := buildJobs(0, 0, 0, 0)
	if len(jobs) != 0 {
		t.Errorf("len(jobs) = %d, want 0", len(jobs))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/genpuzzles/... -run TestBuildJobs -v`
Expected: build failure — `buildJobs` is undefined.

- [ ] **Step 3: Implement jobs.go**

Create `cmd/genpuzzles/jobs.go`:

```go
package main

import "github.com/pustserg/sudoku/internal/sudoku"

// buildJobs returns a slice with one Difficulty value per puzzle to
// generate: easy puzzles first, then medium, then hard, then expert.
func buildJobs(easy, medium, hard, expert int) []sudoku.Difficulty {
	jobs := make([]sudoku.Difficulty, 0, easy+medium+hard+expert)
	for i := 0; i < easy; i++ {
		jobs = append(jobs, sudoku.Easy)
	}
	for i := 0; i < medium; i++ {
		jobs = append(jobs, sudoku.Medium)
	}
	for i := 0; i < hard; i++ {
		jobs = append(jobs, sudoku.Hard)
	}
	for i := 0; i < expert; i++ {
		jobs = append(jobs, sudoku.Expert)
	}
	return jobs
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/genpuzzles/... -run TestBuildJobs -v`
Expected: PASS for `TestBuildJobs` and `TestBuildJobsAllZero`.

- [ ] **Step 5: Replace main.go**

Replace the entire contents of `cmd/genpuzzles/main.go` (the current file is just a two-line stub) with:

```go
// Command genpuzzles generates and rates the offline puzzle pool and
// bulk-loads it into the puzzles table.
package main

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/pustserg/sudoku/internal/config"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/sudoku"
)

func main() {
	easy := flag.Int("easy", 50000, "number of Easy puzzles to generate")
	medium := flag.Int("medium", 50000, "number of Medium puzzles to generate")
	hard := flag.Int("hard", 50000, "number of Hard puzzles to generate")
	expert := flag.Int("expert", 50000, "number of Expert puzzles to generate")
	workers := flag.Int("workers", runtime.NumCPU(), "number of concurrent generator workers")
	batchSize := flag.Int("batch-size", 500, "puzzles buffered per batch insert")
	databaseURL := flag.String("database-url", "", "Postgres connection string (default: DATABASE_URL env var)")
	flag.Parse()

	dsn := *databaseURL
	if dsn == "" {
		dsn = config.Load().DatabaseURL
	}
	if dsn == "" {
		log.Fatal("no database URL: pass --database-url or set DATABASE_URL")
	}

	sqlDB, err := db.Open(dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	if err := checkPuzzlesTableExists(sqlDB); err != nil {
		log.Fatalf("%v (run: go run ./cmd/migrate up)", err)
	}

	jobs := buildJobs(*easy, *medium, *hard, *expert)
	total := len(jobs)
	log.Printf("generating %d puzzles (easy=%d medium=%d hard=%d expert=%d) with %d workers",
		total, *easy, *medium, *hard, *expert, *workers)

	jobCh := make(chan sudoku.Difficulty)
	resultCh := make(chan sudoku.Puzzle)

	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(randomSeed(), randomSeed()))
			for d := range jobCh {
				resultCh <- sudoku.Generate(d, rng)
			}
		}()
	}

	go func() {
		for _, d := range jobs {
			jobCh <- d
		}
		close(jobCh)
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	ctx := context.Background()
	start := time.Now()
	inserted := 0
	batch := make([]sudoku.Puzzle, 0, *batchSize)
	for p := range resultCh {
		batch = append(batch, p)
		if len(batch) >= *batchSize {
			if err := db.InsertPuzzles(ctx, sqlDB, batch); err != nil {
				log.Fatalf("insert batch: %v", err)
			}
			inserted += len(batch)
			batch = batch[:0]
			logProgress(inserted, total, start)
		}
	}
	if len(batch) > 0 {
		if err := db.InsertPuzzles(ctx, sqlDB, batch); err != nil {
			log.Fatalf("insert final batch: %v", err)
		}
		inserted += len(batch)
	}

	log.Printf("done: inserted %d/%d puzzles in %s", inserted, total, time.Since(start))
}

// logProgress logs a progress line every 1000 puzzles inserted, and
// always on the final batch.
func logProgress(inserted, total int, start time.Time) {
	if inserted%1000 != 0 && inserted != total {
		return
	}
	elapsed := time.Since(start)
	log.Printf("inserted %d/%d puzzles (%.1f/sec)", inserted, total, float64(inserted)/elapsed.Seconds())
}

// randomSeed returns a cryptographically random uint64, used to seed each
// worker's independent *rand.Rand so workers never retrace each other's
// generation sequence.
func randomSeed() uint64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		log.Fatalf("read random seed: %v", err)
	}
	return binary.BigEndian.Uint64(b[:])
}

// checkPuzzlesTableExists returns an error if the puzzles table doesn't
// exist yet (migrations haven't been applied).
func checkPuzzlesTableExists(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec("SELECT 1 FROM puzzles LIMIT 0"); err != nil {
		return fmt.Errorf("puzzles table not found: %w", err)
	}
	return nil
}
```

- [ ] **Step 6: Verify it builds**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 7: Verify end-to-end with a small real run against Postgres**

Make sure Postgres is up and migrated (it should be, from Tasks 1/2/4 — if not: `docker compose up -d postgres` then `DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable' go run ./cmd/migrate up`):
Run: `docker compose ps postgres`
Expected: `healthy`.

Note the current row count so you can confirm exactly 8 new rows land:
Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c 'SELECT count(*) FROM puzzles;'`
Expected: some number N (likely 0, since Task 4's integration test cleans up after itself — note N for the next check).

Run a tiny real generation:
Run: `go run ./cmd/genpuzzles --easy=2 --medium=2 --hard=2 --expert=2 --workers=2 --database-url='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable'`
Expected: progress/summary log lines ending in `done: inserted 8/8 puzzles in ...`, no errors. (If fewer than 8 were inserted due to a `UNIQUE(givens)` collision with a pre-existing row, that's only expected if N above was already nonzero and happened to collide — extremely unlikely for a fresh table; treat any shortfall as a real bug to investigate, not something to wave off.)

Verify the rows landed correctly:
Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c 'SELECT difficulty, count(*) FROM puzzles GROUP BY difficulty ORDER BY difficulty;'`
Expected: four rows, one per difficulty (0,1,2,3), each with `count = 2` more than that difficulty had before this run (N was 0 in the fresh-table case, so each shows exactly 2).

Spot-check that a stored puzzle's givens are actually solvable to its stored solution (catches a byte-order or off-by-one bug in the storage format that a smaller unit test might miss):
Run:
```bash
psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c \
  "SELECT givens, solution FROM puzzles WHERE difficulty = 0 LIMIT 1;"
```
Expected: a `givens`/`solution` pair of 81-character digit strings each; visually confirm `solution` has no `0` characters (a solution must be fully filled) while `givens` has at least one `0` (a puzzle must have blanks to solve).

Clean up the test rows so they don't pollute a later real generation run:
Run: `psql "postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable" -c "DELETE FROM puzzles WHERE difficulty IN (0,1,2,3);"`
Expected: `DELETE 8` (or however many rows this task actually added).

Stop the Postgres container (this is the last task in the plan):
Run: `docker compose down`

- [ ] **Step 8: Run the full test suite one more time**

Run: `go test ./... -v`
Expected: all packages PASS (the DB integration test will `SKIP` since Postgres is now stopped and `SUDOKU_TEST_DATABASE_URL` isn't set — that's expected).

Run: `gofmt -l .`
Expected: no output.

- [ ] **Step 9: Commit**

```bash
git add cmd/genpuzzles/jobs.go cmd/genpuzzles/jobs_test.go cmd/genpuzzles/main.go
git commit -m "Add cmd/genpuzzles concurrent bulk puzzle generator"
```
