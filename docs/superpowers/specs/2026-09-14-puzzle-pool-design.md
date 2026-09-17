# Puzzle pool design (`migrations`, `internal/db`, `cmd/genpuzzles`, `cmd/migrate`)

Date: 2026-09-14
Status: Approved for implementation
Part of: Roadmap Phase 2, sub-project 2 of 3 (engine → **puzzle pool** → play loop)

## Purpose

Give the project a persistent pool of pregenerated puzzles, per `ROADMAP.md`
Phase 2 and `AGENTS.md`'s "puzzles are pregenerated, not generated per
request" rule: a `puzzles` table (givens, solution, difficulty) populated
offline by `cmd/genpuzzles`, which reuses `internal/sudoku` (already built
in sub-project 1: `Grid`, `Solve`, `Rate`, `Generate`, `Difficulty`) for
generation. Runtime game creation (sub-project 3, not yet built) will read
a row from this table; it must never call the generator at request time.

This sub-project also introduces `cmd/migrate`, a small command needed to
apply the schema — required plumbing for `AGENTS.md`'s "use golang-migrate"
rule that the original roadmap wording didn't call out explicitly.

## Schema and migrations

`migrations/0001_create_puzzles.up.sql`:

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

`migrations/0001_create_puzzles.down.sql`:

```sql
DROP TABLE puzzles;
```

- `givens`/`solution` are 81-character digit strings, `'0'`-`'9'` (`'0'` =
  empty), row-major (row 0 cols 0-8, then row 1, ...) — the same layout as
  `sudoku.Grid`, just flattened.
- `difficulty` stores `int(sudoku.Difficulty)` directly (0=Easy, 1=Medium,
  2=Hard, 3=Expert), matching the `iota` order defined in sub-project 1.
- `UNIQUE (givens)` cheaply guards against generating the exact same
  puzzle twice across workers or runs.
- The `difficulty` index supports sub-project 3's future "pick a puzzle of
  difficulty X" query; this sub-project does not implement that query
  itself.
- Migration files follow `golang-migrate`'s `{version}_{name}.{up,down}.sql`
  naming convention, since `cmd/migrate` (below) uses that library directly.

## `internal/db`

**Connection:**

```go
func Open(databaseURL string) (*sql.DB, error)
```

Registers and opens via the `jackc/pgx/v5/stdlib` driver (a `database/sql`
driver, keeping `AGENTS.md`'s "`database/sql`, no ORM" rule intact — pgx's
non-stdlib native API, e.g. `CopyFrom`, is deliberately not used). Sets
`SetMaxOpenConns`, `SetConnMaxLifetime` to sane fixed defaults, and calls
`db.Ping` once before returning so callers fail fast on bad configuration
rather than on the first query.

**Grid/string conversion** (pure, unexported, unit-tested without a DB):

```go
func gridToString(g sudoku.Grid) string
func stringToGrid(s string) (sudoku.Grid, error)
```

Flattens/parses the 81-character layout described above. `stringToGrid`
returns an error for a string that isn't exactly 81 digit characters —
this is the only validation `internal/db` does; it trusts `sudoku.Grid`
values passed to `InsertPuzzles` are already valid (the generator
guarantees that).

**Bulk insert:**

```go
func InsertPuzzles(ctx context.Context, db *sql.DB, puzzles []sudoku.Puzzle) error
```

Runs inside one transaction. Batches `puzzles` into chunks of 500 and
issues one multi-row `INSERT INTO puzzles (givens, solution, difficulty)
VALUES ($1,$2,$3),($4,$5,$6),... ON CONFLICT (givens) DO NOTHING` per
chunk — `ON CONFLICT DO NOTHING` means a rare cross-worker duplicate
doesn't fail the whole batch or the run. Commits once at the end; any
per-chunk error rolls back the whole transaction and returns the error
(a partial pool from a failed run is expected to be discarded and
re-generated, not patched up).

**Testing:** `gridToString`/`stringToGrid` get ordinary table-driven unit
tests (no DB needed). `Open` and `InsertPuzzles` get one integration test
gated on the `SUDOKU_TEST_DATABASE_URL` environment variable — skipped via
`t.Skip` when it's unset (the default in CI/sandbox environments without a
live Postgres), runnable locally against the `docker-compose` Postgres
service by setting that variable.

## `cmd/genpuzzles`

Flags:
- `--easy`, `--medium`, `--hard`, `--expert` (int, default 50000 each →
  200000 total, the low end of the roadmap's 200k-300k target — operators
  can raise these for a larger pool).
- `--workers` (int, default `runtime.NumCPU()`).
- `--batch-size` (int, default 500 — must match `internal/db`'s insert
  chunk size conceptually, but is only used here to size the writer's
  buffering; `InsertPuzzles` does its own chunking regardless of the
  slice length passed in).
- `--database-url` (string, default `""`, meaning "use
  `config.Load().DatabaseURL`").

**Structure:**

1. Build a job list: one `sudoku.Difficulty` value per puzzle to generate,
   count taken from the four flags. This construction is a small pure
   function (`buildJobs(easy, medium, hard, expert int) []sudoku.Difficulty`)
   so it's unit-testable without touching goroutines, randomness, or a DB.
2. Feed the jobs through a buffered channel to a worker pool
   (`--workers` goroutines). Each worker owns one `*rand.Rand`, seeded
   uniquely at startup (via `crypto/rand` bytes converted to a `uint64`
   seed pair, one per worker — never a shared or time-based seed, which
   could collide or repeat across workers) so workers never retrace each
   other's generation sequence, and calls `sudoku.Generate(d, rng)` for
   each job it receives.
3. Workers send completed `sudoku.Puzzle` values to a single results
   channel. One writer goroutine reads from it, accumulates into batches
   of `--batch-size`, and calls `db.InsertPuzzles` per batch — keeping all
   database writes on one goroutine avoids transaction contention between
   workers.
4. The writer logs progress every 1000 puzzles inserted (count so far,
   elapsed time, puzzles/sec).
5. `main` opens the DB via `db.Open`, checks the `puzzles` table exists
   (a failed `SELECT 1 FROM puzzles LIMIT 0` with a clear "run
   `cmd/migrate` first" error message) before starting generation, runs
   the pipeline above, waits for all workers and the writer to finish,
   and reports a final summary (total inserted, total attempted, elapsed).

`cmd/genpuzzles` assumes the schema already exists; it never runs
migrations itself.

**Testing:** `buildJobs` gets a table-driven unit test. The
worker-pool/channel plumbing and `main` itself are not unit tested (no
live DB in this environment, and `AGENTS.md` doesn't mandate CLI handler
tests) — this mirrors `cmd/server/main.go`'s existing thin-`main` pattern,
where the testable logic is factored out and `main` just wires it up.

## `cmd/migrate`

A minimal command wrapping `golang-migrate/migrate/v4`:

```
go run ./cmd/migrate up
go run ./cmd/migrate down
```

Uses the library's Postgres driver and file-source driver pointed at
`migrations/`, with the database URL from `config.Load().DatabaseURL`
(no `--database-url` flag needed here — this is an operator command run
against one specific environment at a time, unlike `genpuzzles` which
might target a scratch DB). Prints the resulting migration version on
success. `migrate.ErrNoChange` is treated as a successful no-op, not an
error (running `up` twice should be harmless).

## New dependencies

- `github.com/jackc/pgx/v5` (stdlib driver)
- `github.com/golang-migrate/migrate/v4` (with its `postgres` and `file`
  source/driver subpackages)

Both are standard, widely-used libraries for exactly this purpose;
`AGENTS.md`'s "no ORM" rule is about query abstraction (no
repository/ActiveRecord layer hiding SQL), not about banning a DB driver
or a migration tool — both of those are infrastructure, not an ORM.

## Out of scope for this sub-project

- The runtime "pick a puzzle by difficulty" query and REST API — sub-project 3.
- Automatic migration-on-startup — migrations are a deliberate, separate
  operator action via `cmd/migrate`.
- Re-rating or re-generating existing pool rows — not needed yet.
