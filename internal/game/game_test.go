package game

import (
	"context"
	"sync"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

var ctx = context.Background()

func testPuzzle() sudoku.Puzzle {
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3
	return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy}
}

func mustCreate(t *testing.T, s *Store, p sudoku.Puzzle) *Game {
	t.Helper()
	g, err := s.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	return g
}

func TestStoreCreateAndGet(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if g.ID == "" {
		t.Fatal("Create() returned a game with an empty ID")
	}
	if g.Current != g.Givens {
		t.Errorf("Current = %v, want equal to Givens on creation", g.Current)
	}

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", g.ID, err)
	}
	if *got != *g {
		t.Errorf("Get(%q) returned different game data than Create produced", g.ID)
	}
}

func TestStoreCreateGeneratesUniqueIDs(t *testing.T) {
	s := NewStore()
	g1 := mustCreate(t, s, testPuzzle())
	g2 := mustCreate(t, s, testPuzzle())

	if g1.ID == g2.ID {
		t.Errorf("two Create() calls produced the same ID %q", g1.ID)
	}
}

func TestStoreGetUnknownID(t *testing.T) {
	s := NewStore()
	if _, err := s.Get(ctx, "does-not-exist"); err != ErrNotFound {
		t.Errorf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestStoreApplyMove(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	// testPuzzle has exactly one non-given cell ([0][1]), so filling it
	// with the correct value (3) solves the game.
	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Current[0][1] != 3 {
		t.Errorf("Current[0][1] = %d, want 3", updated.Current[0][1])
	}
	if !updated.Solved() {
		t.Fatalf("Solved() = false after filling in the last cell, want true")
	}

	// Once solved, even a no-op-looking move (clearing the cell back
	// out) must be rejected — see TestApplyMoveRejectedAfterSolved for
	// why this matters beyond the in-memory store.
	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 0); err != ErrGameOver {
		t.Errorf("ApplyMove() clearing cell on a solved game: error = %v, want ErrGameOver", err)
	}
}

func TestStoreApplyMoveOutOfRange(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	tests := []struct {
		name            string
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
			if _, err := s.ApplyMove(ctx, g.ID, tt.row, tt.col, tt.value); err != ErrOutOfRange {
				t.Errorf("ApplyMove(%d,%d,%d) error = %v, want ErrOutOfRange", tt.row, tt.col, tt.value, err)
			}
		})
	}
}

func TestStoreApplyMoveGivenCell(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // (0,0) is a given (value 5)

	if _, err := s.ApplyMove(ctx, g.ID, 0, 0, 7); err != ErrGivenCell {
		t.Errorf("ApplyMove() on a given cell error = %v, want ErrGivenCell", err)
	}
}

func TestStoreApplyMoveUnknownGame(t *testing.T) {
	s := NewStore()
	if _, err := s.ApplyMove(ctx, "does-not-exist", 0, 1, 5); err != ErrNotFound {
		t.Errorf("ApplyMove() on unknown game error = %v, want ErrNotFound", err)
	}
}

func TestGameSolved(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if g.Solved() {
		t.Error("Solved() = true immediately after creation, want false")
	}

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if !updated.Solved() {
		t.Error("Solved() = false after Current matches Solution, want true")
	}
}

func TestGetReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())
	gameID := g.ID

	copy1, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", gameID, err)
	}

	copy1.Current[0][1] = 9
	copy1.Current[5][5] = 7

	copy2, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v after first mutation, want nil", gameID, err)
	}

	if copy2.Current[0][1] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[0][1] = %d, want 0", copy2.Current[0][1])
	}
	if copy2.Current[5][5] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[5][5] = %d, want 0", copy2.Current[5][5])
	}
}

func TestCreateReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())
	gameID := g.ID

	g.Current[0][1] = 9
	g.Current[5][5] = 7

	got, err := s.Get(ctx, gameID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", gameID, err)
	}
	if got.Current[0][1] != 0 {
		t.Errorf("After mutating Create's returned copy, store's copy was affected: Current[0][1] = %d, want 0", got.Current[0][1])
	}
	if got.Current[5][5] != 0 {
		t.Errorf("After mutating Create's returned copy, store's copy was affected: Current[5][5] = %d, want 0", got.Current[5][5])
	}
}

func TestApplyMoveReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}

	updated.Current[0][1] = 9
	updated.Current[5][5] = 7

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get(%q) error = %v, want nil", g.ID, err)
	}
	if got.Current[0][1] != 3 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[0][1] = %d, want 3", got.Current[0][1])
	}
	if got.Current[5][5] != 0 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[5][5] = %d, want 0", got.Current[5][5])
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()

	const sharedGames = 4
	shared := make([]*Game, sharedGames)
	for i := range shared {
		shared[i] = mustCreate(t, s, testPuzzle())
	}

	const workers = 20
	const iterations = 50

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			sharedGame := shared[worker%sharedGames]
			for j := 0; j < iterations; j++ {
				g, err := s.Create(ctx, testPuzzle())
				if err != nil {
					t.Errorf("Create() error = %v, want nil", err)
					continue
				}

				if _, err := s.Get(ctx, g.ID); err != nil {
					t.Errorf("Get(%q) error = %v for a game just created, want nil", g.ID, err)
				}

				if _, err := s.ApplyMove(ctx, g.ID, 0, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", g.ID, err)
				}

				if _, err := s.Get(ctx, sharedGame.ID); err != nil {
					t.Errorf("Get(%q) error = %v for shared game, want nil", sharedGame.ID, err)
				}
				if _, err := s.ApplyMove(ctx, sharedGame.ID, 1, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", sharedGame.ID, err)
				}
			}
		}(i)
	}
	wg.Wait()

	for _, g := range shared {
		got, err := s.Get(ctx, g.ID)
		if err != nil {
			t.Errorf("Get(%q) error = %v after concurrent access, want nil", g.ID, err)
			continue
		}
		if got.Current[1][1] == 0 {
			t.Errorf("shared game %q Current[1][1] = 0 after concurrent ApplyMove calls, want a value written by some goroutine", g.ID)
		}
	}
}

func TestCreateSetsMaxMistakesByDifficulty(t *testing.T) {
	tests := []struct {
		d    sudoku.Difficulty
		want int
	}{
		{sudoku.Easy, 5},
		{sudoku.Medium, 5},
		{sudoku.Hard, 3},
		{sudoku.Expert, 3},
	}
	for _, tt := range tests {
		p := testPuzzle()
		p.Difficulty = tt.d
		s := NewStore()
		g := mustCreate(t, s, p)
		if g.MaxMistakes != tt.want {
			t.Errorf("Create() with difficulty %v: MaxMistakes = %d, want %d", tt.d, g.MaxMistakes, tt.want)
		}
	}
}

func TestApplyMoveWrongValueCountsMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 9) // wrong: solution wants 3
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1 after one wrong entry", updated.Mistakes)
	}
	if updated.Current[0][1] != 9 {
		t.Errorf("Current[0][1] = %d, want 9 (wrong entries still fill the cell)", updated.Current[0][1])
	}
}

func TestApplyMoveCorrectValueDoesNotCountMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 3) // correct
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 0 {
		t.Errorf("Mistakes = %d, want 0 after a correct entry", updated.Mistakes)
	}
}

func TestApplyMoveClearingCellDoesNotCountMistake(t *testing.T) {
	s := NewStore()
	g := mustCreate(t, s, testPuzzle())

	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 9); err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	updated, err := s.ApplyMove(ctx, g.ID, 0, 1, 0) // erase
	if err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1 (erasing should not add a mistake, and should not remove the earlier one)", updated.Mistakes)
	}
}

func TestApplyMoveRejectedAfterGameOver(t *testing.T) {
	s := NewStore()
	p := testPuzzle()
	p.Difficulty = sudoku.Hard // MaxMistakes == 3
	g := mustCreate(t, s, p)

	for i := 0; i < 3; i++ {
		if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 9); err != nil { // always wrong
			t.Fatalf("ApplyMove() error = %v on mistake %d", err, i+1)
		}
	}

	got, err := s.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if !got.Failed() {
		t.Fatalf("Failed() = false after 3 mistakes with MaxMistakes=3, want true")
	}

	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 3); err != ErrGameOver {
		t.Errorf("ApplyMove() after game over: error = %v, want ErrGameOver", err)
	}
}

// TestApplyMoveRejectedAfterSolved guards against a real bug: once a
// game is solved, any further move (even a harmless one) must be
// rejected. Without this check, db.GameStore.ApplyMove would re-derive
// and persist status = 'in_progress' onto an already-solved row, which
// can then collide with the one-active-game-per-difficulty constraint.
func TestApplyMoveRejectedAfterSolved(t *testing.T) {
	s := NewStore()
	p := testPuzzle() // solution[0][0] == 5, solution[0][1] == 3, givens[0][0] == 5
	g := mustCreate(t, s, p)

	got, err := s.ApplyMove(ctx, g.ID, 0, 1, 3) // the only non-given cell; solves the game
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if !got.Solved() {
		t.Fatalf("Solved() = false after filling in the last cell, want true")
	}

	if _, err := s.ApplyMove(ctx, g.ID, 0, 1, 0); err != ErrGameOver {
		t.Errorf("ApplyMove() after game solved: error = %v, want ErrGameOver", err)
	}
}

// compile-time check that *Store implements GameStore.
var _ GameStore = (*Store)(nil)
