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

- **Language**: Go
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
internal/sudoku/     puzzle generation, solving, validation (pure logic)
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

> Project is in early setup — these steps will fill in as the code lands.

```bash
go run ./cmd/server
```

Requires a running PostgreSQL instance (connection string via env var, see
`AGENTS.md` for conventions once configuration lands).

## Testing

- Table-driven unit tests for `internal/sudoku` (generation validity, solver
  correctness).
- Handler tests for `internal/api` using `net/http/httptest`.
- htmx page flows are verified manually in-browser; no browser test suite
  planned for v1.
