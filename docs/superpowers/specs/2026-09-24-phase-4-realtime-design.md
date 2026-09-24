# Phase 4 — Realtime sync

Status: approved by user on 2026-09-24, ready for implementation planning.

## Goal

Add a WebSocket endpoint so that opening the same game in two tabs or
devices shows moves sync instantly, per `ROADMAP.md` Phase 4. Moves are
still submitted exactly as today (`POST /play/{id}/moves` for the web UI,
`POST /api/games/{id}/moves` for the JSON API) — the WebSocket is a
push-only notification channel layered on top, not a new way to change
game state.

## Non-goals

- Collaborative/competitive multiplayer (out of scope per `AGENTS.md`).
- A delta/resync protocol over the WebSocket itself. Reconnects are
  handled by the server always sending the current full board fragment
  as the first message on every (re)connection — see "The WS endpoint"
  below.
- Live sync for the JSON API's consumers (a future mobile client) beyond
  triggering the notification — no JSON payload variant of the push is
  built now; add one later if a JSON-consuming client needs it.
- Horizontal scaling (multiple server processes). The `Hub` is a single
  in-process pub/sub, matching the existing single-process assumption
  (`internal/game.Store` is already an in-memory, single-process map).

## `internal/ws` (new package)

Transport-only: knows about game IDs as opaque strings, nothing about
HTML or `game.Game`. New dependency: `github.com/gorilla/websocket`.

```go
package ws

// Hub is a single-process pub/sub keyed by game ID, safe for concurrent
// use. It carries no payload — a Publish just tells subscribers "game id
// changed," and each subscriber re-fetches/re-renders its own view.
type Hub struct { /* mu sync.Mutex; subs map[string]map[chan struct{}]struct{} */ }

func NewHub() *Hub

// Subscribe registers interest in gameID. The returned channel receives
// a value (non-blocking; a slow/full receiver just misses a signal
// rather than backing up the publisher) on every Publish(gameID) until
// cancel is called. Callers must call cancel when done (on connection
// close) to avoid leaking the subscription.
func (h *Hub) Subscribe(gameID string) (ch <-chan struct{}, cancel func())

// Publish notifies all current subscribers of gameID. A no-op if
// nobody is subscribed.
func (h *Hub) Publish(gameID string)
```

`NotifyingStore` wraps any `game.GameStore` and publishes after every
move that actually changed state:

```go
type NotifyingStore struct {
    game.GameStore
    Hub *Hub
}

func (s NotifyingStore) ApplyMove(ctx context.Context, id string, row, col, value int) (*game.Game, error) {
    g, err := s.GameStore.ApplyMove(ctx, id, row, col, value)
    if err == nil {
        s.Hub.Publish(id)
    }
    return g, err
}
```

This is the one chokepoint: whether a move came in through the REST API
or the web handler, `ApplyMove` succeeding publishes. `internal/ws`
depends on `internal/game` (for the `GameStore` interface); nothing
depends back on `internal/ws` for game logic, keeping `internal/sudoku`
and `internal/game` exactly as dependency-light as before.

`Upgrade`/connection helpers (also in `internal/ws`, thin wrappers
around `gorilla/websocket`):

```go
// ServeSubscriber upgrades r, then loops: sends an initial message via
// initial(), then on every receive from a Hub subscription for gameID,
// sends render()'s result, until the client disconnects or render/initial
// return an error (game deleted, etc. — logged and the socket closed).
// A background ping/pong on a fixed interval detects dead connections
// and unsubscribes/closes them; the (discarded) read loop only exists to
// process pong frames and notice closes.
func ServeSubscriber(w http.ResponseWriter, r *http.Request, hub *Hub, gameID string, render func() ([]byte, error)) error
```

Keeping `render` as a caller-supplied closure is what lets `internal/ws`
stay free of HTML/template concerns while still owning all the
gorilla-specific upgrade/ping/write-loop boilerplate in one place.

## Wiring into `internal/web`

New route: `GET /ws/games/{id}`, handled by `web.Handler` (it already
owns `newBoardView`/template rendering).

```go
func (h *Handler) gameWS(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    store, _, err := h.storeFor(r) // same scoping as showBoard/submitMove
    if err != nil { http.Error(w, "...", http.StatusInternalServerError); return }
    if _, err := store.Get(r.Context(), id); err != nil {
        http.Error(w, "not found", http.StatusNotFound) // pre-upgrade, still a normal HTTP error
        return
    }
    render := func() ([]byte, error) {
        g, err := store.Get(r.Context(), id)
        if err != nil { return nil, err }
        var buf bytes.Buffer
        // oob wrapper so the client's plain WS message handler can just
        // innerHTML/outerHTML it in without special-casing shape
        err = templates.ExecuteTemplate(&buf, "board", newBoardView(g))
        return buf.Bytes(), err
    }
    if err := ws.ServeSubscriber(w, r, h.hub, id, render); err != nil {
        log.Printf("web: ws subscriber for %s: %v", id, err)
    }
}
```

`web.Handler` gains a `hub *ws.Hub` field (constructor param, mirroring
`sqlDB`/`auth`). `storeFor` wraps whatever store it resolves
(`h.anonStore` or `db.NewGameStore(...)`) in `ws.NotifyingStore{..., Hub:
h.hub}` before returning it, so both `web.Handler` and `api.Handler`
(which gains the same `hub` field and the same wrap in its own
`storeFor`) publish through the same hub. `cmd/server/main.go`
constructs one `hub := ws.NewHub()` and passes it to both `api.NewHandler`
and `web.NewHandler`.

Authorization matches `showBoard` today: anonymous games are
"unguessable ID is the access control," logged-in games are scoped by
`storeFor` resolving to a user-scoped `db.GameStore`, so `store.Get`
already fails for another user's game ID. No new authz logic.

## Client (`internal/web/templates/board.html`)

The existing inline `<script>` block gains:

- `applyNotes()` + `applySelection()` calls extracted into one
  `refreshOverlays()` function, called from both the existing
  `htmx:afterSwap` listener and the new WS message handler (avoids
  duplicating that logic).
- On page load, open `new WebSocket(wsURL(gameID))` (derives `ws://` vs
  `wss://` from `location.protocol`). On `message`, replace `#board`'s
  `outerHTML` with the payload, then `refreshOverlays()`.
- On `close` (that isn't a deliberate navigation-away), reconnect after
  a short fixed delay (e.g. 1s) — good enough for a single-user,
  same-network tool; no exponential backoff needed.
- The tab that itself submitted the move already gets its board fragment
  back from the htmx AJAX response; the WS push to that same tab is a
  harmless redundant re-render (idempotent), not specifically suppressed.

## Error handling

- Pre-upgrade failures (auth error, game not found/wrong scope) are
  plain HTTP error responses — no WebSocket upgrade has happened yet.
- Post-upgrade failures (game deleted mid-connection, write error) close
  the socket with a close frame; the client's reconnect loop will then
  get the same pre-upgrade error on its next attempt and stop retrying
  once it sees a non-101 response (simple client-side check: don't
  reconnect after an HTTP-level failure, only after a clean/unclean
  socket close).
- Dead connections (browser closed, network drop) are reaped via
  ping/pong timeout inside `ServeSubscriber`, which calls the
  subscription's `cancel()` so the `Hub` doesn't leak.

## Testing

- `internal/ws`: table-driven/concurrency tests for `Hub` (subscribe,
  publish reaches subscribers, publish with no subscribers is a no-op,
  cancel stops delivery, concurrent Subscribe/Publish/cancel is race-free
  — run with `-race`). Test for `NotifyingStore` (publishes only when the
  wrapped `ApplyMove` returns nil error, propagates the underlying
  store's result unchanged otherwise).
- `internal/api` and `internal/web`: existing handler tests are
  unaffected (a nil or no-op `Hub`/wrap still behaves like the
  unwrapped store); no new handler-test surface since `ApplyMove`'s
  return value is unchanged.
- The actual two-tabs-syncing behavior, reconnect-after-drop, and
  ping/pong timeout are verified manually in-browser, same as the rest
  of the htmx UI per `AGENTS.md`; note this in the PR description.
- `go test ./... -race` and `gofmt -l .` clean before considering the
  phase done.

## Git workflow

Per `AGENTS.md`: developed on its own branch (e.g. `phase-4-realtime`),
not on `main`, merged only when the user explicitly asks.
