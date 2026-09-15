package game

import (
	"sync"
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
	if *got != *g {
		t.Errorf("Get(%q) returned different game data than Create produced", g.ID)
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

	updated, err := s.ApplyMove(g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	if !updated.Solved() {
		t.Error("Solved() = false after Current matches Solution, want true")
	}
}

func TestGetReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())
	gameID := g.ID

	// Get a copy from the store
	copy1, ok := s.Get(gameID)
	if !ok {
		t.Fatalf("Get(%q) ok = false, want true", gameID)
	}

	// Mutate the returned copy directly (this should not affect the store's internal copy)
	copy1.Current[0][1] = 9
	copy1.Current[5][5] = 7

	// Get another copy from the store
	copy2, ok := s.Get(gameID)
	if !ok {
		t.Fatalf("Get(%q) ok = false after first mutation, want true", gameID)
	}

	// The store's internal copy should be unaffected by mutations to copy1
	if copy2.Current[0][1] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[0][1] = %d, want 0", copy2.Current[0][1])
	}
	if copy2.Current[5][5] != 0 {
		t.Errorf("After mutating returned copy, store's copy was affected: Current[5][5] = %d, want 0", copy2.Current[5][5])
	}
}

func TestCreateReturnsIndependentCopy(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())
	gameID := g.ID

	// Mutate the copy returned by Create directly.
	g.Current[0][1] = 9
	g.Current[5][5] = 7

	// A fresh Get should not reflect that mutation.
	got, ok := s.Get(gameID)
	if !ok {
		t.Fatalf("Get(%q) ok = false, want true", gameID)
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
	g := s.Create(testPuzzle())

	updated, err := s.ApplyMove(g.ID, 0, 1, 3)
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}

	// Mutate the copy returned by ApplyMove directly.
	updated.Current[0][1] = 9
	updated.Current[5][5] = 7

	// A fresh Get should not reflect that mutation (except the move we
	// actually applied through ApplyMove itself).
	got, ok := s.Get(g.ID)
	if !ok {
		t.Fatalf("Get(%q) ok = false, want true", g.ID)
	}
	if got.Current[0][1] != 3 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[0][1] = %d, want 3", got.Current[0][1])
	}
	if got.Current[5][5] != 0 {
		t.Errorf("After mutating ApplyMove's returned copy, store's copy was affected: Current[5][5] = %d, want 0", got.Current[5][5])
	}
}

// TestStoreConcurrentAccess drives the Store from many goroutines at once
// so that `go test -race` can catch data races like the one previously
// found and fixed in Get/Create/ApplyMove (they used to return live
// pointers into the store's internal map). Some goroutines share a game
// ID so concurrent Get and ApplyMove calls actually race on the same
// game's Current grid, which is exactly the scenario the original bug
// affected.
func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()

	// A handful of games shared across goroutines, to force concurrent
	// Get/ApplyMove calls to collide on the same underlying Game.
	const sharedGames = 4
	shared := make([]*Game, sharedGames)
	for i := range shared {
		shared[i] = s.Create(testPuzzle())
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
				// Create a new game each iteration to exercise Create
				// under concurrency too.
				g := s.Create(testPuzzle())

				if _, ok := s.Get(g.ID); !ok {
					t.Errorf("Get(%q) ok = false for a game just created, want true", g.ID)
				}

				// testPuzzle()'s solution has 0 at (0,1) unless the value
				// happens to be 3, so most of these count as mistakes;
				// tolerate ErrGameOver once a game's mistake cap is hit,
				// which is now correct behavior, not a race.
				if _, err := s.ApplyMove(g.ID, 0, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", g.ID, err)
				}

				// Concurrently read and write the shared game to
				// specifically exercise the Get+ApplyMove race on one
				// Game's Current grid.
				if _, ok := s.Get(sharedGame.ID); !ok {
					t.Errorf("Get(%q) ok = false for shared game, want true", sharedGame.ID)
				}
				if _, err := s.ApplyMove(sharedGame.ID, 1, 1, (j%9)+1); err != nil && err != ErrGameOver {
					t.Errorf("ApplyMove(%q) error = %v, want nil or ErrGameOver", sharedGame.ID, err)
				}
			}
		}(i)
	}
	wg.Wait()

	// Sanity check: the store is left in a coherent state and ordinary
	// operations still work after the concurrent hammering.
	for _, g := range shared {
		got, ok := s.Get(g.ID)
		if !ok {
			t.Errorf("Get(%q) ok = false after concurrent access, want true", g.ID)
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
		g := s.Create(p)
		if g.MaxMistakes != tt.want {
			t.Errorf("Create() with difficulty %v: MaxMistakes = %d, want %d", tt.d, g.MaxMistakes, tt.want)
		}
	}
}

func TestApplyMoveWrongValueCountsMistake(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(g.ID, 0, 1, 9) // wrong: solution wants 3
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
	g := s.Create(testPuzzle()) // solution[0][1] == 3

	updated, err := s.ApplyMove(g.ID, 0, 1, 3) // correct
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 0 {
		t.Errorf("Mistakes = %d, want 0 after a correct entry", updated.Mistakes)
	}
}

func TestApplyMoveClearingCellDoesNotCountMistake(t *testing.T) {
	s := NewStore()
	g := s.Create(testPuzzle())

	if _, err := s.ApplyMove(g.ID, 0, 1, 9); err != nil {
		t.Fatalf("ApplyMove() error = %v", err)
	}
	updated, err := s.ApplyMove(g.ID, 0, 1, 0) // erase
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
	g := s.Create(p)

	for i := 0; i < 3; i++ {
		if _, err := s.ApplyMove(g.ID, 0, 1, 9); err != nil { // always wrong
			t.Fatalf("ApplyMove() error = %v on mistake %d", err, i+1)
		}
	}

	got, ok := s.Get(g.ID)
	if !ok {
		t.Fatalf("Get() ok = false")
	}
	if !got.Failed() {
		t.Fatalf("Failed() = false after 3 mistakes with MaxMistakes=3, want true")
	}

	if _, err := s.ApplyMove(g.ID, 0, 1, 3); err != ErrGameOver {
		t.Errorf("ApplyMove() after game over: error = %v, want ErrGameOver", err)
	}
}
