// Package game holds the in-memory game session store shared by both
// the REST API and the htmx web UI, per AGENTS.md's rule that they must
// use the same service layer rather than diverging.
package game

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// Game is one in-progress (or completed) puzzle session.
type Game struct {
	ID          string
	Givens      sudoku.Grid // original clues; never mutated after creation
	Current     sudoku.Grid // the player's working grid
	Solution    sudoku.Grid
	Difficulty  sudoku.Difficulty
	Mistakes    int
	MaxMistakes int
}

// Solved reports whether Current matches Solution exactly.
func (g *Game) Solved() bool {
	return g.Current == g.Solution
}

// Failed reports whether the player has used up all their allowed
// mistakes for this game.
func (g *Game) Failed() bool {
	return g.Mistakes >= g.MaxMistakes
}

// MaxMistakesFor returns the mistake allowance for a given difficulty:
// 5 for Easy/Medium, 3 for Hard/Expert.
func MaxMistakesFor(d sudoku.Difficulty) int {
	if d == sudoku.Hard || d == sudoku.Expert {
		return 3
	}
	return 5
}

var (
	ErrOutOfRange = errors.New("row/col must be 0-8 and value must be 0-9")
	ErrGivenCell  = errors.New("cannot change a given cell")
	ErrNotFound   = errors.New("game not found")
	ErrGameOver   = errors.New("game is over: mistake limit reached")
)

// GameStore is the interface both the in-memory Store (anonymous play)
// and the Postgres-backed db.GameStore (logged-in play) implement, so
// internal/api and internal/web can depend on the interface and pick an
// implementation per request without duplicating move-handling code.
type GameStore interface {
	Create(ctx context.Context, p sudoku.Puzzle) (*Game, error)
	Get(ctx context.Context, id string) (*Game, error)
	ApplyMove(ctx context.Context, id string, row, col, value int) (*Game, error)
}

// ApplyMove mutates g in place according to a player move (0 clears the
// cell) and returns the sentinel error to reject it with, or nil on
// success. value must be 0-9; row and col must be 0-8. A cell that is
// non-zero in g.Givens cannot be changed. Placing a non-zero value that
// doesn't match g.Solution still fills the cell (so the player can see
// what they entered) but counts as a mistake. No further moves are
// accepted once g.Failed() or g.Solved() is already true — this matters
// beyond the in-memory store: db.GameStore.ApplyMove re-derives and
// persists status from the game state on every move, so an accepted
// move on an already-solved game would write status back to
// 'in_progress' on a solved row. This is the single place
// move-validation rules live — both GameStore implementations call it
// after loading their own copy of the Game.
func ApplyMove(g *Game, row, col, value int) error {
	if row < 0 || row > 8 || col < 0 || col > 8 || value < 0 || value > 9 {
		return ErrOutOfRange
	}
	if g.Failed() || g.Solved() {
		return ErrGameOver
	}
	if g.Givens[row][col] != 0 {
		return ErrGivenCell
	}
	if value != 0 && value != g.Solution[row][col] {
		g.Mistakes++
	}
	g.Current[row][col] = value
	return nil
}

// Store holds all in-progress games in memory, safe for concurrent use.
// It implements GameStore and backs anonymous (not-logged-in) play.
type Store struct {
	mu    sync.Mutex
	games map[string]*Game
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{games: make(map[string]*Game)}
}

// Create starts a new Game from a freshly pulled puzzle and returns it.
// Returns an independent copy to prevent data races from concurrent
// access. The context is accepted to satisfy GameStore; the in-memory
// store never uses it.
func (s *Store) Create(ctx context.Context, p sudoku.Puzzle) (*Game, error) {
	g := &Game{
		ID:          NewID(),
		Givens:      p.Givens,
		Current:     p.Givens,
		Solution:    p.Solution,
		Difficulty:  p.Difficulty,
		MaxMistakes: MaxMistakesFor(p.Difficulty),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games[g.ID] = g
	result := *g
	return &result, nil
}

// Get returns the game with the given ID, or ErrNotFound if it doesn't
// exist. Returns an independent copy to prevent data races from
// concurrent access.
func (s *Store) Get(ctx context.Context, id string) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.games[id]
	if !ok {
		return nil, ErrNotFound
	}
	copy := *g
	return &copy, nil
}

// ApplyMove applies a move to the game with the given id via the shared
// ApplyMove rules and returns the updated Game. Returns ErrNotFound if
// id doesn't exist. Returns an independent copy to prevent data races
// from concurrent access.
func (s *Store) ApplyMove(ctx context.Context, id string, row, col, value int) (*Game, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.games[id]
	if !ok {
		return nil, ErrNotFound
	}
	if err := ApplyMove(g, row, col, value); err != nil {
		return nil, err
	}
	copy := *g
	return &copy, nil
}

// NewID returns a random, opaque, unguessable-enough identifier, used
// both for game IDs and (by internal/auth) session tokens.
// crypto/rand.Read failing indicates the environment itself is broken
// (no source of randomness available); there is no sane recovery, so
// this panics rather than returning a predictable or empty ID.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("game: failed to read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// PuzzleLookup returns a random puzzle of difficulty d. Production code
// passes db.RandomPuzzle (adapted to this signature); tests pass a stub.
type PuzzleLookup func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error)
