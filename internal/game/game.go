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

// maxMistakesFor returns the mistake allowance for a given difficulty:
// 5 for Easy/Medium, 3 for Hard/Expert.
func maxMistakesFor(d sudoku.Difficulty) int {
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

// Store holds all in-progress games in memory, safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	games map[string]*Game
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{games: make(map[string]*Game)}
}

// Create starts a new Game from a freshly pulled puzzle and returns it.
// Returns an independent copy to prevent data races from concurrent access.
func (s *Store) Create(p sudoku.Puzzle) *Game {
	g := &Game{
		ID:          newID(),
		Givens:      p.Givens,
		Current:     p.Givens,
		Solution:    p.Solution,
		Difficulty:  p.Difficulty,
		MaxMistakes: maxMistakesFor(p.Difficulty),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.games[g.ID] = g
	result := *g
	return &result
}

// Get returns the game with the given ID, or ok=false if it doesn't exist.
// Returns an independent copy to prevent data races from concurrent access.
func (s *Store) Get(id string) (*Game, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.games[id]
	if !ok {
		return nil, false
	}
	copy := *g
	return &copy, true
}

// ApplyMove sets Current[row][col] = value (0 clears the cell) on the
// game with the given id, and returns the updated Game. value must be
// 0-9; row and col must be 0-8. A cell that is non-zero in Givens cannot
// be changed. Placing a non-zero value that doesn't match Solution still
// fills the cell (so the player can see what they entered) but counts as
// a mistake; once the game's mistake allowance is used up, further moves
// are rejected with ErrGameOver. Returns an independent copy to prevent
// data races from concurrent access.
func (s *Store) ApplyMove(id string, row, col, value int) (*Game, error) {
	if row < 0 || row > 8 || col < 0 || col > 8 || value < 0 || value > 9 {
		return nil, ErrOutOfRange
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.games[id]
	if !ok {
		return nil, ErrNotFound
	}
	if g.Failed() {
		return nil, ErrGameOver
	}
	if g.Givens[row][col] != 0 {
		return nil, ErrGivenCell
	}
	if value != 0 && value != g.Solution[row][col] {
		g.Mistakes++
	}
	g.Current[row][col] = value
	copy := *g
	return &copy, nil
}

// newID returns a random, opaque, unguessable-enough game identifier.
// crypto/rand.Read failing indicates the environment itself is broken
// (no source of randomness available); there is no sane recovery, so
// this panics rather than returning a predictable or empty ID.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("game: failed to read random bytes: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// PuzzleLookup returns a random puzzle of difficulty d. Production code
// passes db.RandomPuzzle (adapted to this signature); tests pass a stub.
type PuzzleLookup func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error)
