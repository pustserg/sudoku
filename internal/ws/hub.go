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
