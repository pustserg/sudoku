# Phase 4 (Realtime Sync) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a WebSocket endpoint so the same game open in two tabs/devices stays in sync, without changing how moves are submitted.

**Architecture:** A new transport-only `internal/ws` package holds a single-process pub/sub `Hub` (keyed by game ID, no payload) plus a `NotifyingStore` decorator that publishes after every successful move, and a `ServeSubscriber` helper that upgrades a request, sends a caller-supplied render on connect and on every publish, and reaps dead connections via ping/pong. `internal/web` owns the one HTTP endpoint (`GET /ws/games/{id}`) and the HTML rendering; `internal/api` only needs to wrap its store so moves made through the JSON API also notify web viewers.

**Tech Stack:** Go 1.27, `github.com/gorilla/websocket` (new dependency), existing htmx/vanilla-JS client.

**Spec:** `docs/superpowers/specs/2026-09-24-phase-4-realtime-design.md`

## Global Constraints

- `internal/ws` must not import `internal/web` or `internal/api` (no import cycles) and must not know about HTML/templates — only `internal/game` for the `GameStore` interface.
- No new way to change game state: moves still only go through the existing `POST .../moves` endpoints.
- `go test ./... -race`, `gofmt -l .`, and `go vet ./...` must be clean before the phase is done.
- Per `AGENTS.md`: all work happens on branch `phase-4-realtime`, never merged to `main` unless the user explicitly asks.

---

### Task 1: `internal/ws.Hub`

**Files:**
- Create: `internal/ws/hub.go`
- Test: `internal/ws/hub_test.go`

**Interfaces:**
- Produces: `type Hub struct{...}`, `func NewHub() *Hub`, `func (h *Hub) Subscribe(gameID string) (ch <-chan struct{}, cancel func())`, `func (h *Hub) Publish(gameID string)`.

- [ ] **Step 1: Create the branch**

```bash
git checkout -b phase-4-realtime
```

- [ ] **Step 2: Write the failing tests**

Create `internal/ws/hub_test.go`:

```go
package ws

import (
	"sync"
	"testing"
	"time"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("game-1")
	defer cancel()

	h.Publish("game-1")

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("did not receive notification after Publish")
	}
}

func TestPublishToUnknownGameIsNoop(t *testing.T) {
	h := NewHub()
	h.Publish("no-such-game") // must not panic
}

func TestPublishDoesNotReachOtherGames(t *testing.T) {
	h := NewHub()
	chA, cancelA := h.Subscribe("game-a")
	defer cancelA()
	chB, cancelB := h.Subscribe("game-b")
	defer cancelB()

	h.Publish("game-a")

	select {
	case <-chA:
	case <-time.After(time.Second):
		t.Fatal("game-a subscriber did not receive notification")
	}
	select {
	case <-chB:
		t.Fatal("game-b subscriber received a notification meant for game-a")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCancelStopsDelivery(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("game-1")
	cancel()

	h.Publish("game-1") // must not panic or block, even though ch is now unsubscribed

	select {
	case <-ch:
		t.Fatal("received a notification after cancel")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestConcurrentSubscribePublishCancel(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := h.Subscribe("game-1")
			defer cancel()
			h.Publish("game-1")
			select {
			case <-ch:
			case <-time.After(time.Second):
			}
		}()
	}
	h.Publish("game-1")
	wg.Wait()
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/ws/... -race`
Expected: FAIL to build — `undefined: NewHub` (package `internal/ws` doesn't exist yet).

- [ ] **Step 4: Implement the Hub**

Create `internal/ws/hub.go`:

```go
// Package ws provides a single-process, transport-only notification hub
// for "game state changed" events, plus the WebSocket plumbing that
// turns those events into a live connection. It knows nothing about
// game.Game or HTML — callers supply their own rendering.
package ws

import "sync"

// Hub is a pub/sub keyed by game ID, safe for concurrent use. Publish
// carries no payload: it just tells subscribers "gameID changed," and
// each subscriber is expected to re-fetch/re-render its own view.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewHub returns an empty Hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[chan struct{}]struct{})}
}

// Subscribe registers interest in gameID. The returned channel receives
// a value on every Publish(gameID) until cancel is called; cancel is
// idempotent and safe to call more than once. The channel is buffered
// (size 1) and a send never blocks — a subscriber that hasn't drained a
// previous notification just misses this one rather than backing up
// Publish, since the calling code always re-fetches current state
// rather than relying on a stream of individual events.
func (h *Hub) Subscribe(gameID string) (ch <-chan struct{}, cancel func()) {
	c := make(chan struct{}, 1)
	h.mu.Lock()
	if h.subs[gameID] == nil {
		h.subs[gameID] = make(map[chan struct{}]struct{})
	}
	h.subs[gameID][c] = struct{}{}
	h.mu.Unlock()

	cancel = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subs[gameID], c)
		if len(h.subs[gameID]) == 0 {
			delete(h.subs, gameID)
		}
	}
	return c, cancel
}

// Publish notifies all current subscribers of gameID. A no-op if
// nobody is subscribed.
func (h *Hub) Publish(gameID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[gameID] {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ws/... -race -v`
Expected: PASS (all 5 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/ws/hub.go internal/ws/hub_test.go
git commit -m "Add internal/ws.Hub, a single-process game-change pub/sub"
```

---

### Task 2: `internal/ws.NotifyingStore`

**Files:**
- Create: `internal/ws/store.go`
- Test: `internal/ws/store_test.go`

**Interfaces:**
- Consumes: `game.GameStore` interface (`Create`, `Get`, `ApplyMove` — `internal/game/game.go:58-62`), `Hub.Subscribe`/`Publish` from Task 1.
- Produces: `type NotifyingStore struct { game.GameStore; Hub *Hub }` with an `ApplyMove` override — later tasks wrap a real store with `ws.NotifyingStore{GameStore: store, Hub: hub}` and use it as a plain `game.GameStore`.

- [ ] **Step 1: Write the failing tests**

Create `internal/ws/store_test.go`:

```go
package ws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// fakeStore is a minimal game.GameStore whose ApplyMove result is
// controlled per test; Create/Get are never exercised here.
type fakeStore struct {
	applyMoveResult *game.Game
	applyMoveErr    error
}

func (f fakeStore) Create(ctx context.Context, p sudoku.Puzzle) (*game.Game, error) {
	panic("not used in these tests")
}

func (f fakeStore) Get(ctx context.Context, id string) (*game.Game, error) {
	panic("not used in these tests")
}

func (f fakeStore) ApplyMove(ctx context.Context, id string, row, col, value int) (*game.Game, error) {
	return f.applyMoveResult, f.applyMoveErr
}

func TestNotifyingStorePublishesOnSuccess(t *testing.T) {
	hub := NewHub()
	ch, cancel := hub.Subscribe("game-1")
	defer cancel()

	g := &game.Game{ID: "game-1"}
	store := NotifyingStore{GameStore: fakeStore{applyMoveResult: g}, Hub: hub}

	got, err := store.ApplyMove(context.Background(), "game-1", 0, 0, 5)
	if err != nil {
		t.Fatalf("ApplyMove() error: %v", err)
	}
	if got != g {
		t.Errorf("ApplyMove() = %v, want the wrapped store's result unchanged", got)
	}

	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("Hub was not published to after a successful ApplyMove")
	}
}

func TestNotifyingStoreDoesNotPublishOnError(t *testing.T) {
	hub := NewHub()
	ch, cancel := hub.Subscribe("game-1")
	defer cancel()

	wantErr := errors.New("boom")
	store := NotifyingStore{GameStore: fakeStore{applyMoveErr: wantErr}, Hub: hub}

	_, err := store.ApplyMove(context.Background(), "game-1", 0, 0, 5)
	if !errors.Is(err, wantErr) {
		t.Fatalf("ApplyMove() error = %v, want %v", err, wantErr)
	}

	select {
	case <-ch:
		t.Fatal("Hub was published to after a failed ApplyMove")
	case <-time.After(50 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ws/... -race`
Expected: FAIL to build — `undefined: NotifyingStore`.

- [ ] **Step 3: Implement NotifyingStore**

Create `internal/ws/store.go`:

```go
package ws

import (
	"context"

	"github.com/pustserg/sudoku/internal/game"
)

// NotifyingStore wraps a game.GameStore and publishes to Hub after
// every move that actually changed state. Whether a move came in
// through the REST API or the web handler, this is the one chokepoint
// that notifies any WebSocket subscribers watching that game.
type NotifyingStore struct {
	game.GameStore
	Hub *Hub
}

// ApplyMove delegates to the wrapped store and, only on success,
// publishes id to Hub. The wrapped store's result and error are
// returned unchanged either way.
func (s NotifyingStore) ApplyMove(ctx context.Context, id string, row, col, value int) (*game.Game, error) {
	g, err := s.GameStore.ApplyMove(ctx, id, row, col, value)
	if err == nil {
		s.Hub.Publish(id)
	}
	return g, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/ws/... -race -v`
Expected: PASS (all tests in the package).

- [ ] **Step 5: Commit**

```bash
git add internal/ws/store.go internal/ws/store_test.go
git commit -m "Add ws.NotifyingStore to publish on every successful move"
```

---

### Task 3: `internal/ws.ServeSubscriber`

**Files:**
- Modify: `go.mod`, `go.sum` (add `github.com/gorilla/websocket`)
- Create: `internal/ws/serve.go`
- Test: `internal/ws/serve_test.go`

**Interfaces:**
- Consumes: `Hub` from Task 1.
- Produces: `func ServeSubscriber(w http.ResponseWriter, r *http.Request, hub *Hub, gameID string, render func() ([]byte, error)) error` — later tasks call this from an `http.HandlerFunc`.

- [ ] **Step 1: Add the dependency**

```bash
go get github.com/gorilla/websocket
go mod tidy
```

- [ ] **Step 2: Write the failing test**

Create `internal/ws/serve_test.go`:

```go
package ws

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestServeSubscriberSendsInitialAndOnPublish(t *testing.T) {
	hub := NewHub()
	renderCount := 0
	render := func() ([]byte, error) {
		renderCount++
		return []byte(fmt.Sprintf("render-%d", renderCount)), nil
	}

	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		done <- ServeSubscriber(w, r, hub, "game-1", render)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial() error: %v", err)
	}
	defer conn.Close()

	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error: %v", err)
	}
	if string(msg) != "render-1" {
		t.Fatalf("initial message = %q, want %q", msg, "render-1")
	}

	hub.Publish("game-1")

	_, msg, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() after publish error: %v", err)
	}
	if string(msg) != "render-2" {
		t.Fatalf("message after publish = %q, want %q", msg, "render-2")
	}

	conn.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeSubscriber did not return after the client closed the connection")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/ws/... -race`
Expected: FAIL to build — `undefined: ServeSubscriber`.

- [ ] **Step 4: Implement ServeSubscriber**

Create `internal/ws/serve.go`:

```go
package ws

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// This is a same-origin browser client talking to its own server,
	// not a cross-site API; the connection carries no ambient
	// credential the server hasn't already scoped via storeFor before
	// upgrading, so a permissive origin check adds no risk here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

const (
	pingInterval = 30 * time.Second
	pongWait     = 60 * time.Second
	writeWait    = 10 * time.Second
)

// ServeSubscriber upgrades r to a WebSocket, sends render()'s result
// immediately (so a fresh or reconnecting client is in sync with no
// separate resync step), then sends render()'s result again every time
// hub publishes for gameID. It blocks until the connection ends (client
// disconnect, a render/write failure, or a ping timeout) and returns
// the error that ended it (nil for a clean client-initiated close).
// The caller's ResponseWriter must not have been written to yet.
func ServeSubscriber(w http.ResponseWriter, r *http.Request, hub *Hub, gameID string, render func() ([]byte, error)) error {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, cancel := hub.Subscribe(gameID)
	defer cancel()

	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	// The client never sends application messages, but ReadMessage must
	// still run so pong frames are processed and a client-initiated
	// close is detected. It runs in its own goroutine so it doesn't
	// block the send loop below; conn.Close() (deferred above, and
	// reached whenever this function returns) makes ReadMessage return
	// an error, ending this goroutine.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	send := func() error {
		payload, err := render()
		if err != nil {
			return err
		}
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteMessage(websocket.TextMessage, payload)
	}

	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-closed:
			return nil
		case <-ch:
			if err := send(); err != nil {
				return err
			}
		case <-ticker.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return err
			}
		}
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/ws/... -race -v`
Expected: PASS (all tests in the package).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/ws/serve.go internal/ws/serve_test.go
git commit -m "Add ws.ServeSubscriber: WebSocket upgrade, ping/pong, publish-driven pushes"
```

---

### Task 4: Wire the hub into `internal/api`

**Files:**
- Modify: `internal/api/handler.go`
- Modify: `internal/api/handler_test.go:41,64`

**Interfaces:**
- Consumes: `ws.NotifyingStore`, `ws.Hub` from Tasks 1-3.
- Produces: `api.NewHandler` gains a trailing `hub *ws.Hub` parameter (nil-safe).

- [ ] **Step 1: Update `NewHandler`'s two test call sites first (so the build stays green mid-edit is not required, but this keeps the diff obviously mechanical)**

```bash
sed -i '' '41s/nil, nil), store/nil, nil, nil), store/' internal/api/handler_test.go
sed -i '' '64s/authSvc, sqlDB)/authSvc, sqlDB, nil)/' internal/api/handler_test.go
```

Verify: `grep -n "NewHandler(" internal/api/handler_test.go` should show:
```
return NewHandler(store, lookup, nil, nil, nil), store
h = NewHandler(store, lookup, authSvc, sqlDB, nil)
```

- [ ] **Step 2: Modify `internal/api/handler.go`**

Add the import (alongside the existing `internal/...` imports):

```go
	"github.com/pustserg/sudoku/internal/ws"
```

Replace the `Handler` struct and `NewHandler`:

```go
// Handler serves the JSON REST API for creating and playing games, plus
// Google OAuth login/logout for API (e.g. future mobile) clients.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
	hub       *ws.Hub
}

// NewHandler returns a Handler. anonStore backs anonymous play; puzzles
// pulls new puzzles; auth and sqlDB back Google login and per-user game
// persistence. auth and sqlDB may be nil in tests that don't exercise
// the authenticated path — storeFor then always falls back to
// anonStore, and the auth endpoints are not expected to be called. hub
// may also be nil in tests that don't exercise realtime sync — wrap
// then returns the store unwrapped.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB, hub *ws.Hub) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB, hub: hub}
}
```

Replace `storeFor` and add `wrap` right after it:

```go
func (h *Handler) storeFor(r *http.Request) (game.GameStore, int64, error) {
	if h.auth == nil {
		return h.wrap(h.anonStore), 0, nil
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequestBearerOnly(r))
	if err == nil {
		return h.wrap(db.NewGameStore(h.sqlDB, user.ID)), user.ID, nil
	}
	if authFallbackToAnon(err) {
		return h.wrap(h.anonStore), 0, nil
	}
	return nil, 0, err
}

// wrap makes a successful ApplyMove on store publish to h.hub, so any
// WebSocket subscriber watching that game (via internal/web) is
// notified regardless of whether the move came in through this JSON
// API or the htmx web handler. A nil h.hub (tests that don't wire one)
// makes this a passthrough.
func (h *Handler) wrap(store game.GameStore) game.GameStore {
	if h.hub == nil {
		return store
	}
	return ws.NotifyingStore{GameStore: store, Hub: h.hub}
}
```

- [ ] **Step 3: Update `cmd/server/main.go`'s call site so the build compiles (full change lands in Task 6, but a stale signature would break `go build ./...` now)**

Modify `cmd/server/main.go:57`:

```go
	api.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB, nil).Register(mux)
```

(The real hub is threaded through in Task 6; `nil` here keeps the tree building without pre-empting that task's wiring.)

- [ ] **Step 4: Run the build and tests**

Run: `go build ./... && go test ./internal/api/... -race`
Expected: build succeeds, all `internal/api` tests PASS unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/api/handler.go internal/api/handler_test.go cmd/server/main.go
git commit -m "Wrap internal/api's GameStore so moves notify WebSocket subscribers"
```

---

### Task 5: Wire the hub and the WebSocket route into `internal/web`

**Files:**
- Modify: `internal/web/web.go`
- Modify: `internal/web/web_test.go:118,174,257,290,310,346`

**Interfaces:**
- Consumes: `ws.NotifyingStore`, `ws.Hub`, `ws.ServeSubscriber` from Tasks 1-3; `newBoardView(g *game.Game) boardView` and `templates` (both already in `internal/web/web.go`).
- Produces: `web.NewHandler` gains a trailing `hub *ws.Hub` parameter; new route `GET /ws/games/{id}`.

- [ ] **Step 1: Update the six existing test call sites**

```bash
sed -i '' '118s/secure)/secure, nil)/' internal/web/web_test.go
sed -i '' '174s/sqlDB, true)/sqlDB, true, nil)/' internal/web/web_test.go
sed -i '' '257s/sqlDB, false)/sqlDB, false, nil)/' internal/web/web_test.go
sed -i '' '290s/nil, false)/nil, false, nil)/' internal/web/web_test.go
sed -i '' '310s/nil, false)/nil, false, nil)/' internal/web/web_test.go
sed -i '' '346s/sqlDB, false)/sqlDB, false, nil)/' internal/web/web_test.go
```

Verify: `grep -n "NewHandler(" internal/web/web_test.go` should show all six calls with a trailing `, nil)`.

- [ ] **Step 2: Modify `internal/web/web.go` imports**

Add to the existing import block:

```go
	"bytes"
```
```go
	"github.com/pustserg/sudoku/internal/ws"
```

- [ ] **Step 3: Update the `Handler` struct and `NewHandler`**

```go
// Handler serves the htmx web UI, including Google OAuth login/logout.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
	// secureCookies sets the Secure attribute on the session and OAuth
	// state cookies. It must be false for local HTTP development
	// (http://localhost:...) — a Secure cookie is never sent back by the
	// browser over plain HTTP, which would break login entirely — and
	// true in any real deployment, which is always HTTPS. Callers derive
	// this from whether the configured Google OAuth redirect URL is
	// https://, rather than hardcoding it, so dev and prod both work
	// without a separate flag.
	secureCookies bool
	hub           *ws.Hub
}

// NewHandler returns a Handler. See api.NewHandler's doc comment for
// the meaning of anonStore/puzzles/authSvc/sqlDB/hub — the two packages
// mirror each other for those. secureCookies controls the Secure
// attribute on cookies this Handler sets; see the Handler.secureCookies
// field doc for how callers should derive it.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB, secureCookies bool, hub *ws.Hub) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB, secureCookies: secureCookies, hub: hub}
}
```

- [ ] **Step 4: Add the new route in `Register`**

```go
// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /stats", h.stats)
	mux.HandleFunc("POST /play", h.createGame)
	mux.HandleFunc("GET /play/{id}", h.showBoard)
	mux.HandleFunc("POST /play/{id}/moves", h.submitMove)
	mux.HandleFunc("GET /ws/games/{id}", h.gameWS)
	mux.HandleFunc("GET /auth/google/login", h.googleLogin)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /logout", h.logout)
}
```

- [ ] **Step 5: Update `storeFor` and add `wrap`, mirroring `internal/api`**

```go
func (h *Handler) storeFor(r *http.Request) (game.GameStore, int64, error) {
	if h.auth == nil {
		return h.wrap(h.anonStore), 0, nil
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err == nil {
		return h.wrap(db.NewGameStore(h.sqlDB, user.ID)), user.ID, nil
	}
	if authFallbackToAnon(err) {
		return h.wrap(h.anonStore), 0, nil
	}
	return nil, 0, err
}

// wrap mirrors api.Handler.wrap: makes a successful ApplyMove on store
// publish to h.hub. A nil h.hub (tests that don't wire one) makes this
// a passthrough.
func (h *Handler) wrap(store game.GameStore) game.GameStore {
	if h.hub == nil {
		return store
	}
	return ws.NotifyingStore{GameStore: store, Hub: h.hub}
}
```

- [ ] **Step 6: Add the `gameWS` handler**

Add near `showBoard`/`submitMove`:

```go
// gameWS upgrades to a WebSocket that pushes the "board" template
// fragment for id every time the game changes (including the moment of
// connecting, so a fresh or reconnecting client is immediately in
// sync — see the Phase 4 design spec's "The WS endpoint" section).
// Authorization mirrors showBoard: storeFor resolves the caller's
// scoped store, and store.Get failing (wrong scope, or the id doesn't
// exist) is rejected before the WebSocket upgrade happens.
func (h *Handler) gameWS(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	store, _, err := h.storeFor(r)
	if err != nil {
		log.Printf("web: authenticate failed: %v", err)
		http.Error(w, "could not authenticate request", http.StatusInternalServerError)
		return
	}
	if _, err := store.Get(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}

	render := func() ([]byte, error) {
		g, err := store.Get(r.Context(), id)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		if err := templates.ExecuteTemplate(&buf, "board", newBoardView(g)); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	if err := ws.ServeSubscriber(w, r, h.hub, id, render); err != nil {
		log.Printf("web: ws subscriber for game %s: %v", id, err)
	}
}
```

- [ ] **Step 7: Update `cmd/server/main.go`'s call site so the build compiles**

Modify `cmd/server/main.go:58`:

```go
	web.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB, secureCookies, nil).Register(mux)
```

- [ ] **Step 8: Run the build and tests**

Run: `go build ./... && go test ./internal/web/... -race`
Expected: build succeeds, all `internal/web` tests PASS unchanged.

- [ ] **Step 9: Commit**

```bash
git add internal/web/web.go internal/web/web_test.go cmd/server/main.go
git commit -m "Add GET /ws/games/{id} and wrap internal/web's GameStore for live sync"
```

---

### Task 6: Wire a real `Hub` into `cmd/server/main.go`

**Files:**
- Modify: `cmd/server/main.go`

**Interfaces:**
- Consumes: `ws.NewHub() *Hub` (Task 1), `api.NewHandler(..., hub *ws.Hub)` (Task 4), `web.NewHandler(..., hub *ws.Hub)` (Task 5).

- [ ] **Step 1: Add the import**

```go
	"github.com/pustserg/sudoku/internal/ws"
```

- [ ] **Step 2: Construct the hub and pass it to both handlers**

Replace:

```go
	anonStore := game.NewStore()
	puzzleLookup := game.PuzzleLookup(func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		return db.RandomPuzzle(ctx, sqlDB, d)
	})
```

with:

```go
	anonStore := game.NewStore()
	puzzleLookup := game.PuzzleLookup(func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		return db.RandomPuzzle(ctx, sqlDB, d)
	})
	hub := ws.NewHub()
```

Replace the two `.Register(mux)` lines (Tasks 4/5 left these passing `nil`):

```go
	api.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB, hub).Register(mux)
	web.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB, secureCookies, hub).Register(mux)
```

- [ ] **Step 3: Build and run the full test suite**

Run: `go build ./... && go test ./... -race`
Expected: build succeeds, all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "Wire a shared ws.Hub into the server so API and web moves stay in sync"
```

---

### Task 7: Client-side live updates (`internal/web/templates/board.html`)

**Files:**
- Modify: `internal/web/templates/board.html`

**Interfaces:**
- Consumes: `GET /ws/games/{id}` from Task 5, which pushes the same "board" fragment markup `submitValue`'s htmx swap already uses.

- [ ] **Step 1: Extract a shared `refreshOverlays` function**

In the inline `<script>` block, find the `htmx:afterSwap` listener:

```javascript
  document.body.addEventListener("htmx:afterSwap", function () {
    if (selected) {
      var cell = findCell(selected.row, selected.col);
      if (cell) {
        var value = parseInt(cell.dataset.value, 10);
        if (value && !cell.classList.contains("wrong") && !cell.classList.contains("given")) {
          clearDigitFromPeerNotes(selected.row, selected.col, value);
        }
      }
    }
    applyNotes();
    applySelection();
  });

  applyNotes();
})();
```

Replace it with:

```javascript
  function refreshOverlays() {
    applyNotes();
    applySelection();
  }

  document.body.addEventListener("htmx:afterSwap", function () {
    if (selected) {
      var cell = findCell(selected.row, selected.col);
      if (cell) {
        var value = parseInt(cell.dataset.value, 10);
        if (value && !cell.classList.contains("wrong") && !cell.classList.contains("given")) {
          clearDigitFromPeerNotes(selected.row, selected.col, value);
        }
      }
    }
    refreshOverlays();
  });

  function wsURL(id) {
    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    return proto + "//" + location.host + "/ws/games/" + id;
  }

  function connectWS() {
    var socket = new WebSocket(wsURL(gameID));
    socket.addEventListener("message", function (e) {
      var board = document.getElementById("board");
      if (!board) return;
      board.outerHTML = e.data;
      refreshOverlays();
    });
    socket.addEventListener("close", function () {
      setTimeout(connectWS, 1000);
    });
  }

  refreshOverlays();
  connectWS();
})();
```

(`gameID` is already defined earlier in this same script as `var gameID = {{.ID}};`.)

- [ ] **Step 2: Manually verify in two tabs**

Start the server (`go run ./cmd/server`, with Postgres running and a puzzle pool loaded per README), open the same `/play/{id}` URL in two browser tabs, and enter a digit in one tab. Confirm:
- The other tab's board updates within about a second, without a manual refresh.
- Entering a digit in a `Given` (locked) cell in either tab is still rejected the same as before (no regression).
- Killing the server and restarting it makes both tabs reconnect within a couple of seconds and resume syncing (watch the browser dev tools Network/WS tab for the reconnect).

- [ ] **Step 3: Commit**

```bash
git add internal/web/templates/board.html
git commit -m "Sync the board live over WebSocket in the htmx UI

Manually verified in two tabs per AGENTS.md's htmx-testing note: moves
in one tab appear in the other without a refresh, and the client
reconnects after the server restarts."
```

---

### Task 8: Final verification and roadmap update

**Files:**
- Modify: `ROADMAP.md`

- [ ] **Step 1: Run the full gate**

```bash
go test ./... -race
gofmt -l .
go vet ./...
```

Expected: `go test` all PASS, `gofmt -l .` prints nothing, `go vet` reports nothing.

- [ ] **Step 2: Mark Phase 4 shipped**

In `ROADMAP.md`, under `## Phase 4 — Realtime sync`, add a status line matching Phase 3's style (right after the heading, before the bullet list):

```markdown
**Status: shipped.**

```

- [ ] **Step 3: Commit**

```bash
git add ROADMAP.md
git commit -m "Mark Phase 4 (realtime sync) shipped"
```

- [ ] **Step 4: Report back to the user**

Summarize what was built and confirm the branch (`phase-4-realtime`) is ready for the user to review/merge — per `AGENTS.md`, do not merge to `main` without them explicitly asking.
