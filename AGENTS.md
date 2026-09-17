# AGENTS.md

Instructions for AI coding agents working in this repository.

## Project summary

Sudoku game: Go backend, htmx web UI, JSON REST API (also intended for a
future mobile client), WebSockets for live single-player sync across
tabs/devices, OAuth login, PostgreSQL persistence. See `README.md` for the
full feature and tech-stack overview.

## Architecture rules

- **Game logic lives in `internal/sudoku` only.** Puzzle generation,
  validation, and solving must have no I/O, no HTTP, no DB dependencies —
  it's plain Go, unit-testable in isolation, and is the single source of
  truth used by both `internal/api` and `internal/ws`. Don't duplicate
  game-logic decisions in handler code.
- **The REST API is the one source of truth for game state changes.** htmx
  handlers in `internal/web` should call into the same internal service
  layer the REST API uses — don't build a second, divergent code path for
  the web UI. This keeps the API ready for the future mobile client without
  extra work.
- **No ORM.** Use `database/sql` with explicit queries in `internal/db`.
  Keep SQL close to the query, not hidden behind generic repository
  abstractions.
- **Puzzles are pregenerated, not generated per request.** The `puzzles`
  table (givens, solution, difficulty) is populated offline by the
  `cmd/genpuzzles` CLI, which reuses `internal/sudoku` for generation,
  uniqueness checking, and difficulty rating. Runtime game creation reads a
  row from `puzzles`; it must not call the generator at request time.
  Difficulty rating logic belongs in `internal/sudoku` so both the CLI and
  any future re-rating tooling use the same rules.
- **Auth**: OAuth via `golang.org/x/oauth2`. Currently Google only; GitHub
  login is deferred to a future phase. Web UI uses a signed, httpOnly session
  cookie; API requests (for future mobile) use a bearer token. Don't implement
  password-based auth — it's out of scope by design.
- **Migrations**: use `golang-migrate`, add a new migration file per schema
  change, never hand-edit an already-applied migration. Migrations run
  automatically when the server starts; `cmd/migrate` remains available for
  manual use or running migrations down if needed.

## Git workflow

- Each roadmap phase (see `ROADMAP.md`) is developed on its own git branch,
  not on `main`.
- Never commit or merge to `main` unless the user explicitly asks for it.
  Work stays on its phase branch until the user says otherwise.

## Conventions

- Standard Go project layout (`cmd/`, `internal/`). Code outside
  `internal/sudoku` may import the standard library and approved
  dependencies (see below) but should not reach into another internal
  package's private details — talk through exported functions/types only.
- Follow standard Go formatting (`gofmt`) and idiomatic error handling
  (return errors, don't panic in library code).
- Keep `internal/sudoku` dependency-free (standard library only).

## Testing expectations

- Any change to `internal/sudoku` needs table-driven unit tests covering the
  change (generation validity, solver correctness, edge cases like already
  full/invalid boards).
- Any change to `internal/api` needs a handler test using
  `net/http/httptest`.
- htmx UI changes are not covered by an automated browser suite for v1 —
  note in the PR/commit description that the flow was checked manually.
- Run `go test ./...` before considering a task done.

## What not to do

- Don't add collaborative/competitive multiplayer, leaderboards, or
  password auth — explicitly out of scope for v1 (see README).
- Don't introduce an ORM or a second query layer.
- Don't bypass the REST/service layer from the htmx handlers.
- Don't commit secrets (OAuth client secrets, DB credentials, session
  signing keys) — use environment variables and keep a `.env.example`
  (without real values) if one is added.
