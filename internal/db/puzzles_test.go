package db

import (
	"context"
	"os"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestChunkPuzzles(t *testing.T) {
	puzzles := make([]sudoku.Puzzle, 7)

	chunks := chunkPuzzles(puzzles, 3)

	if len(chunks) != 3 {
		t.Fatalf("len(chunks) = %d, want 3", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 || len(chunks[2]) != 1 {
		t.Errorf("chunk sizes = %d, %d, %d, want 3, 3, 1", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestChunkPuzzlesEmpty(t *testing.T) {
	chunks := chunkPuzzles(nil, 3)
	if len(chunks) != 0 {
		t.Errorf("len(chunks) = %d, want 0 for empty input", len(chunks))
	}
}

func TestChunkPuzzlesExactMultiple(t *testing.T) {
	puzzles := make([]sudoku.Puzzle, 6)
	chunks := chunkPuzzles(puzzles, 3)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	if len(chunks[0]) != 3 || len(chunks[1]) != 3 {
		t.Errorf("chunk sizes = %d, %d, want 3, 3", len(chunks[0]), len(chunks[1]))
	}
}

// testDatabaseURL returns the SUDOKU_TEST_DATABASE_URL environment
// variable, skipping the calling test if it's unset — these tests need a
// real, already-migrated Postgres database (see Task 4's verification
// steps for how to set one up) and don't run without one.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("SUDOKU_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SUDOKU_TEST_DATABASE_URL not set, skipping integration test")
	}
	return url
}

func TestOpenAndInsertPuzzles(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	defer sqlDB.Close()

	ctx := context.Background()
	var zeroGrid sudoku.Grid
	t.Cleanup(func() {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(zeroGrid)); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	// An all-zero grid is a fine, deterministic fixture here: it's not a
	// real puzzle, but InsertPuzzles doesn't validate puzzle content,
	// only stores what it's given, and Cleanup above guarantees no other
	// test run leaves one behind to collide with this one's uniqueness
	// constraint.
	puzzle := sudoku.Puzzle{
		Givens:     zeroGrid,
		Solution:   zeroGrid,
		Difficulty: sudoku.Easy,
	}
	if err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle}); err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(zeroGrid)).Scan(&count); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	// Inserting the same givens again must be a no-op (ON CONFLICT DO
	// NOTHING), not an error.
	if err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle}); err != nil {
		t.Fatalf("InsertPuzzles() duplicate insert error: %v", err)
	}
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(zeroGrid)).Scan(&count); err != nil {
		t.Fatalf("query after duplicate insert: %v", err)
	}
	if count != 1 {
		t.Errorf("count after duplicate insert = %d, want 1 (ON CONFLICT DO NOTHING should prevent a second row)", count)
	}
}
