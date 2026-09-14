package game

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func testPuzzle() sudoku.Puzzle {
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3
	return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy}
}

func TestStoreCreateAndGet(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())

	if g.ID == "" {
		t.Fatal("Create() returned a game with an empty ID")
	}
	if g.Current != g.Givens {
		t.Errorf("Current = %v, want equal to Givens on creation", g.Current)
	}

	got, ok := s.Get(g.ID)
	if !ok {
		t.Fatalf("Get(%q) ok = false, want true", g.ID)
	}
	if got != g {
		t.Errorf("Get(%q) returned a different *Game than Create produced", g.ID)
	}
}

func TestStoreCreateGeneratesUniqueIDs(t *testing.T) {
	s := NewStore()
	g1 := s.Create(testPuzzle())
	g2 := s.Create(testPuzzle())

	if g1.ID == g2.ID {
		t.Errorf("two Create() calls produced the same ID %q", g1.ID)
	}
}

func TestStoreGetUnknownID(t *testing.T) {
	s := NewStore()
	if _, ok := s.Get("does-not-exist"); ok {
		t.Error("Get() ok = true for an unknown ID, want false")
	}
}

func TestStoreApplyMove(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())

	updated, err := s.ApplyMove(g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Current[0][1] != 3 {
		t.Errorf("Current[0][1] = %d, want 3", updated.Current[0][1])
	}

	updated, err = s.ApplyMove(g.ID, 0, 1, 0)
	if err != nil {
		t.Fatalf("ApplyMove() clearing cell error = %v, want nil", err)
	}
	if updated.Current[0][1] != 0 {
		t.Errorf("Current[0][1] = %d, want 0 after clearing", updated.Current[0][1])
	}
}

func TestStoreApplyMoveOutOfRange(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())

	tests := []struct {
		name           string
		row, col, value int
	}{
		{"row too low", -1, 0, 1},
		{"row too high", 9, 0, 1},
		{"col too low", 0, -1, 1},
		{"col too high", 0, 9, 1},
		{"value too low", 0, 1, -1},
		{"value too high", 0, 1, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.ApplyMove(g.ID, tt.row, tt.col, tt.value); err != ErrOutOfRange {
				t.Errorf("ApplyMove(%d,%d,%d) error = %v, want ErrOutOfRange", tt.row, tt.col, tt.value, err)
			}
		})
	}
}

func TestStoreApplyMoveGivenCell(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle()) // (0,0) is a given (value 5)

	if _, err := s.ApplyMove(g.ID, 0, 0, 7); err != ErrGivenCell {
		t.Errorf("ApplyMove() on a given cell error = %v, want ErrGivenCell", err)
	}
}

func TestStoreApplyMoveUnknownGame(t *testing.T) {
	s := NewStore()
	if _, err := s.ApplyMove("does-not-exist", 0, 1, 5); err != ErrNotFound {
		t.Errorf("ApplyMove() on unknown game error = %v, want ErrNotFound", err)
	}
}

func TestGameSolved(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())

	if g.Solved() {
		t.Error("Solved() = true immediately after creation, want false")
	}

	if _, err := s.ApplyMove(g.ID, 0, 1, 3); err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if !g.Solved() {
		t.Error("Solved() = false after Current matches Solution, want true")
	}
}
