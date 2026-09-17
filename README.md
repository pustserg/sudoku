# Sudoku

A multiplayer-ready Sudoku game with a Go backend, an htmx-driven web UI, and a
JSON REST API designed to also serve a future mobile app.

## Features (v1 scope)

- Play Sudoku puzzles drawn from a pregenerated pool (~200-300k puzzles
  across Easy/Medium/Hard/Expert), with validation and solving support.
- User accounts via OAuth (Google/GitHub) — no passwords to manage.
- Saved games per user, persisted in PostgreSQL.
- Live sync: open the same game in two tabs or devices and moves update in
  real time over WebSockets.

Not in scope for v1: collaborative or competitive multiplayer (two users
solving the same puzzle together), leaderboards.

## Tech stack

- **Language**: Go 1.27+ (set `GOTOOLCHAIN=auto` — `go env -w GOTOOLCHAIN=auto` — so `go build`/`go get` can fetch a matching toolchain automatically if your installed Go is older)
- **Web UI**: server-rendered HTML with [htmx](https://htmx.org/)
- **API**: JSON REST, consumed by the same htmx pages today and by a future
  mobile client later
- **Realtime**: WebSockets, used for live single-player sync across
  devices/tabs
- **Auth**: OAuth (Google/GitHub) via `golang.org/x/oauth2`, session cookie
  for the web UI, bearer token for API clients
- **Database**: PostgreSQL, migrations via `golang-migrate`
- **No ORM**: plain `database/sql` with hand-written queries

## Project layout

```
cmd/server/         server entrypoint
cmd/genpuzzles/      one-off CLI: generates and rates the puzzle pool
cmd/migrate/         applies migrations/ via golang-migrate
internal/sudoku/     puzzle generation, solving, validation (pure logic)
internal/game/         shared in-memory game-session store (used by both api and web)
internal/api/         REST handlers
internal/ws/           WebSocket hub/connection handling
internal/auth/         OAuth + session logic
internal/db/           Postgres access
internal/web/           htmx page handlers + templates/
migrations/
```

`internal/sudoku` has no I/O dependencies — it's the single source of truth
for game logic, used by both the REST API/WebSocket layer and the
`genpuzzles` CLI.

## Puzzle pool

Puzzles aren't generated on the fly per request. A one-off CLI
(`cmd/genpuzzles`) generates ~200,000-300,000 puzzles offline, rates each
one's difficulty (Easy/Medium/Hard/Expert), and bulk-loads them into a
`puzzles` table (givens, solution, difficulty). Starting a game just picks
a row matching the requested difficulty — fast, and difficulty is verified
offline rather than guessed at request time.

## Getting started

**1. Start PostgreSQL** (via the included `docker-compose.yml`):

```bash
docker compose up -d postgres
docker compose ps postgres   # wait until it reports "healthy"
```

**2. Point at the database and apply migrations:**

```bash
export DATABASE_URL='postgres://sudoku:sudoku@localhost:5432/sudoku?sslmode=disable'
go run ./cmd/migrate up
```

**3. Generate a puzzle pool.** The defaults (50,000 puzzles per difficulty)
are meant for a real deployment; for local testing, a much smaller pool is
enough:

```bash
go run ./cmd/genpuzzles --easy=20 --medium=20 --hard=20 --expert=20 --workers=4
```

Run `go run ./cmd/genpuzzles --help` to see all flags (counts per
difficulty, `--workers`, `--batch-size`, `--database-url`). Re-running it is
safe — it only adds new puzzles (`givens` is unique per puzzle, so
re-inserting the exact same puzzle is a no-op).

**4. Start the server:**

```bash
export PORT=8080
go run ./cmd/server
```

Then open **http://localhost:8080/** to play, or use the JSON API directly:

```bash
curl -X POST localhost:8080/api/games -H 'Content-Type: application/json' -d '{"difficulty":"easy"}'
curl localhost:8080/api/games/<id>
curl -X POST localhost:8080/api/games/<id>/moves -H 'Content-Type: application/json' -d '{"row":0,"col":1,"value":4}'
```

Game state itself is in-memory for now (per the current roadmap phase), so
restarting `cmd/server` loses all in-progress games — only the puzzle pool
persists in Postgres.

**Cleanup:** `docker compose down` (add `-v` to also wipe the Postgres data
volume, which removes the puzzle pool too).

## Testing

- Table-driven unit tests for `internal/sudoku` (generation validity, solver
  correctness) and `internal/game` (move validation, mistakes, concurrency).
- Handler tests for `internal/api` using `net/http/httptest`.
- htmx page flows are verified manually in-browser; no browser test suite
  planned for v1.

```bash
go test ./...              # unit tests (Postgres-backed tests skip if
                            # SUDOKU_TEST_DATABASE_URL isn't set)
go test ./... -race        # same, with the race detector
SUDOKU_TEST_DATABASE_URL=$DATABASE_URL go test ./internal/db/... -v
gofmt -l .                 # formatting check
go vet ./...                 # static checks
```
