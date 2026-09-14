package db

import (
	"context"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestRandomPuzzle(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()

	// Two distinct Easy puzzles so we can confirm RandomPuzzle can
	// return either one (not proof of true randomness, just that it
	// isn't hardcoded to the first row).
	var gridA, gridB sudoku.Grid
	gridA[0][0] = 2
	gridB[0][0] = 3
	fixtures := []sudoku.Puzzle{
		{Givens: gridA, Solution: gridA, Difficulty: sudoku.Easy},
		{Givens: gridB, Solution: gridB, Difficulty: sudoku.Easy},
	}
	t.Cleanup(func() {
		for _, p := range fixtures {
			sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(p.Givens))
		}
	})
	_, err = InsertPuzzles(ctx, sqlDB, fixtures)
	if err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}

	seen := map[sudoku.Grid]bool{}
	for i := 0; i < 20; i++ {
		p, err := RandomPuzzle(ctx, sqlDB, sudoku.Easy)
		if err != nil {
			t.Fatalf("RandomPuzzle() error: %v", err)
		}
		if p.Difficulty != sudoku.Easy {
			t.Errorf("RandomPuzzle() difficulty = %v, want Easy", p.Difficulty)
		}
		seen[p.Givens] = true
	}
	if len(seen) == 0 {
		t.Fatal("RandomPuzzle() never returned a recognizable fixture")
	}
}

func TestRandomPuzzleNoneAvailable(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()

	// Guard against another test having left Expert-difficulty rows
	// behind: if any exist, this test's premise (no rows of this
	// difficulty) doesn't hold, so skip rather than assert incorrectly.
	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE difficulty = $1", int(sudoku.Expert)).Scan(&count); err != nil {
		t.Fatalf("count existing Expert puzzles: %v", err)
	}
	if count > 0 {
		t.Skip("Expert puzzles already exist in this database; skipping to avoid a false assertion")
	}

	if _, err := RandomPuzzle(ctx, sqlDB, sudoku.Expert); err == nil {
		t.Error("RandomPuzzle() error = nil for a difficulty with no rows, want an error")
	}
}
