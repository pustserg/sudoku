# Phase 3 — Accounts and persistence

Status: approved by user on 2026-09-17, ready for implementation planning.

## Goal

Add OAuth login (Google only, for now) and persistent, resumable games for
logged-in users, per `ROADMAP.md` Phase 3, while leaving anonymous play
exactly as it works today (in-memory, not persisted).

## Non-goals

- GitHub login (README/AGENTS.md mention it, but this phase ships Google
  only; GitHub can be added later behind the same `auth` interfaces).
- Claiming an anonymous in-progress game on login.
- A saved-games list/history UI (completed games are kept as rows, but
  there's no browsing UI for them yet).
- Anything already out of scope per `AGENTS.md` (multiplayer, leaderboards,
  password auth, ORM).

## Schema changes (new migration `0002_accounts.up.sql` / `.down.sql`)

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
    id            TEXT PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    difficulty    TEXT NOT NULL,
    givens        JSONB NOT NULL,
    current       JSONB NOT NULL,
    solution      JSONB NOT NULL,
    mistakes      INT NOT NULL DEFAULT 0,
    max_mistakes  INT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'in_progress', -- in_progress|solved|failed
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- enforces "one active game per difficulty" at the DB level
CREATE UNIQUE INDEX games_one_active_per_difficulty
    ON games (user_id, difficulty)
    WHERE status = 'in_progress';
```

`givens`/`current`/`solution` are stored as JSONB (a `sudoku.Grid` is a
`[9][9]int`); `internal/db` already has grid <-> DB conversion helpers in
`convert.go` for the `puzzles` table — reuse/extend that, don't add a
second convention.

`sessions.token_hash` stores SHA-256 of the opaque token, never the raw
token — mirrors "never commit secrets" spirit from `AGENTS.md`, and means
a leaked DB backup doesn't hand out live sessions.

## `internal/auth`

- `Config` (from `internal/config`, new fields `GoogleClientID`,
  `GoogleClientSecret`, `GoogleRedirectURL`) wires an
  `oauth2.Config` using `golang.org/x/oauth2/google` endpoints, scopes
  `openid`, `email`.
- `Service` (constructed with the oauth2 config + `*sql.DB`):
  - `LoginURL(state string) string` — wraps `oauth2.Config.AuthCodeURL`.
  - `HandleCallback(ctx, code string) (token string, err error)` — exchanges
    the code, calls Google's userinfo endpoint, upserts the `users` row
    (`ON CONFLICT (provider, provider_user_id) DO UPDATE SET email = ...`),
    generates a new opaque session token (`crypto/rand`, 32 bytes,
    hex-encoded — same pattern as `game.newID`), stores its hash + a fixed
    expiry (30 days) in `sessions`, returns the raw token to the caller.
  - `Authenticate(ctx, token string) (*User, error)` — hashes the token,
    looks up `sessions` joined to `users`, checks `expires_at`; returns
    `auth.ErrNoSession` (not found/expired) for the anonymous case, which
    callers treat as "proceed anonymously," not as an error to surface.
  - `Logout(ctx, token string) error` — deletes the session row.
- `User struct { ID int64; Email string }`.
- CSRF/state: standard OAuth `state` param, stored in a short-lived
  httpOnly cookie set on `LoginURL` redirect and checked on callback.

## Middleware (shared by `internal/api` and `internal/web`)

A small `auth.FromRequest(r *http.Request) string` helper reads the
session token from the `session` cookie (web) or `Authorization: Bearer`
header (API) — no middleware chain needed given the existing handler
style (`http.HandleFunc` with no middleware stack today); each handler
that needs the user calls `h.auth.Authenticate(ctx, auth.FromRequest(r))`
and treats `auth.ErrNoSession` as "anonymous," any other error as 500.

## `GameStore` interface (`internal/game`)

```go
type GameStore interface {
    Create(ctx context.Context, p sudoku.Puzzle) (*Game, error)
    Get(ctx context.Context, id string) (*Game, error)
    ApplyMove(ctx context.Context, id string, row, col, value int) (*Game, error)
}
```

- Move-validation (bounds check, given-cell check, mistake counting,
  solved/failed detection) is extracted from today's
  `Store.ApplyMove` into a pure, unexported helper
  `applyMove(g *Game, row, col, value int) error` that mutates a `*Game`
  in place and returns one of the existing sentinel errors. Both
  implementations below call it, so the rules live in exactly one place.
- `game.Store` (existing in-memory map) is adapted to the new interface
  signature (adds `context.Context` parameters, unused internally) — used
  for anonymous play, unchanged behavior.
- New `db.GameStore` (`internal/db/games.go`) implements the same
  interface against the `games` table:
  - `Create`: if an `in_progress` row already exists for
    `(user_id, difficulty)`, return it instead of inserting (implements
    "resume instead of restart"); otherwise insert a new row.
  - `Get`: select by id (scoped to the caller's `user_id`, so one user
    can't fetch another's game by guessing an id).
  - `ApplyMove`: select the row, run the shared `applyMove` helper, then
    update `current`, `mistakes`, `status` (flips to `solved`/`failed`
    when terminal), `updated_at`. Single-row read-modify-write inside a
    transaction to avoid a race on concurrent moves for the same game.

## Handler wiring

- `api.Handler` and `web.Handler` each gain an `auth *auth.Service` field
  and change their `store` field from `*game.Store` to `game.GameStore`.
- Per-request, handlers resolve the store to use:
  ```go
  store := h.anonStore // existing in-memory *game.Store
  if user, err := h.auth.Authenticate(ctx, auth.FromRequest(r)); err == nil {
      store = h.dbStoreFor(user) // db.GameStore scoped to user.ID
  }
  ```
- New routes:
  - `GET /auth/google/login` (web) — redirect to Google.
  - `GET /auth/google/callback` (web) — exchange code, set `session`
    cookie, redirect to `/`.
  - `POST /api/auth/google/callback` (API/mobile) — exchange code
    (posted `{"code": "..."}`), return `{"token": "..."}`.
  - `POST /api/auth/logout` / `POST /logout` — clear session.
- `cmd/server/main.go`: build `auth.Service` from `cfg` (fail fast at
  startup if `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` are unset, same
  style as the existing `DATABASE_URL` check), pass into both handlers
  alongside the existing in-memory `game.Store`.

## `internal/config` additions

```go
type Config struct {
    Port               string
    DatabaseURL        string
    GoogleClientID     string
    GoogleClientSecret string
    GoogleRedirectURL  string
}
```
Loaded the same way as `Port`/`DatabaseURL` (`getEnv`, no fallback for the
three Google fields — empty means "OAuth disabled," which only matters
for local dev without credentials; `main.go` fails fast once these are
required). Real values already exist in the user's local `.env`
(`GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`); this phase adds
`GOOGLE_REDIRECT_URL` to `.env`/`.env.example` and wires all three into
`Config`.

## Auto-run migrations on server start

Today, `docker compose up` / `go run ./cmd/migrate up` is a separate manual
step before `go run ./cmd/server` (see README "Getting started" steps 2-4).
Since this phase adds new migrations, `cmd/server/main.go` now runs them
automatically on startup, using the same `golang-migrate` library
`cmd/migrate` already uses (`migrate.New("file://migrations", cfg.DatabaseURL)`,
`.Up()`, ignoring `migrate.ErrNoChange`) — no new dependency, one shared
code path. A real error (bad SQL, dirty migration state) still fails
server startup fast, same as the existing `DATABASE_URL` check. This
replaces manual step 2 in the README's "Getting started" section (the
`cmd/migrate` binary itself stays, for `down` and any manual/CI use); the
Makefile's `run` target no longer needs to run migrate first either.

## Testing

- `internal/game`: existing `ApplyMove` tests move to exercise the
  extracted `applyMove` helper directly; `game.Store` tests otherwise
  unchanged aside from added `context.Context` args.
- `internal/db`: table-driven tests for `db.GameStore` against a real
  Postgres (matching `puzzles_test.go`'s pattern) — create, get, apply
  move, resume-instead-of-duplicate, and the one-active-per-difficulty
  constraint.
- `internal/auth`: `Service` tests against a `Google`-shaped
  `httptest.Server` stubbing the token and userinfo endpoints (no real
  Google calls in tests) — covers login → callback → session issuance →
  authenticate → logout.
- `internal/api`: `httptest`-based handler tests for the new auth
  endpoints and for the create/get/move endpoints under both an
  authenticated and an anonymous request, confirming the right
  `GameStore` is used.
- htmx UI changes (login/logout links, "Continue"/"New game" per
  difficulty) checked manually per `AGENTS.md`'s existing rule; note this
  in the PR description.
- `go test ./...` green before considering the phase done.

## Git workflow

Per `AGENTS.md`: developed on its own branch (e.g. `phase-3-accounts`),
not on `main`, merged only when the user explicitly asks.
