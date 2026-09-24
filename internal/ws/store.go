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
