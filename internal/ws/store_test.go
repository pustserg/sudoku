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
