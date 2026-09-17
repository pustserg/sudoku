# Roadmap

Phased build-out for the Sudoku project, bottom-up: get the core game logic
solid first, then layer on UI, persistence, and realtime sync. Each phase
should be shippable/demoable on its own before moving to the next.

## Phase 1 — Foundation

- Go module setup, `cmd/server` entrypoint, base project layout
  (`internal/sudoku`, `internal/api`, `internal/web`, `internal/db`,
  `internal/auth`, `internal/ws`, `migrations/`).
- Local dev setup: run PostgreSQL (e.g. via Docker), env var configuration,
  basic health-check endpoint.
- CI: `go test ./...` and `gofmt` check on push.

## Phase 2 — Core game and puzzle pool

- `internal/sudoku`: puzzle generation, uniqueness checking, difficulty
  rating, and solver, with table-driven unit tests.
- `puzzles` table + migration (givens, solution, difficulty).
  `cmd/genpuzzles` CLI generates and bulk-loads ~200k-300k puzzles across
  Easy/Medium/Hard/Expert, run once offline (not at request time).
- REST API: create a new game (pulls a puzzle row by difficulty), fetch
  game state, submit a move, check solved status — game state itself can
  stay in-memory at this stage; only the puzzle pool needs Postgres.
- htmx web UI: render a board, play a puzzle end-to-end through the API, no
  accounts yet (anonymous/local play).

## Phase 3 — Accounts and persistence

**Status: shipped.**

- PostgreSQL schema + migrations: `users`, `games` (added alongside the
  existing `puzzles` table).
- OAuth login via Google (GitHub login deferred to a future phase), session
  cookie for the web UI, bearer token support on the API for future mobile use.
- Save/resume games per logged-in user; anonymous play still works but
  isn't persisted.
- Migrations run automatically on server startup.

## Phase 4 — Realtime sync

- WebSocket endpoint (`internal/ws`) for live game state updates.
- Open the same game in two tabs/devices and see moves sync instantly.
- Reconnect/resync handling (e.g. client rejoins after a dropped
  connection).

## Phase 5 — Polish and mobile-readiness

- API review for mobile-client fitness: consistent error format, pagination
  where needed, versioning if warranted.
- UX polish on the htmx UI (difficulty selection, mistakes/hints if
  wanted, basic styling).
- Handler tests for `internal/api` (`net/http/httptest`) filled out to
  cover the full surface.
- Deployment story (how/where this actually runs).

## Explicitly out of scope for now

See `AGENTS.md` — collaborative/competitive multiplayer, leaderboards,
password-based auth, ORM.
