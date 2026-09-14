package db

import (
	"context"
	"fmt"
	"os"
	"strings"
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
	// t.Cleanup callbacks run in LIFO order (most-recently-registered
	// first), so register the close first and the delete second: the
	// delete then runs before the close, letting it actually reach the
	// database instead of failing against an already-closed connection.
	t.Cleanup(func() {
		sqlDB.Close()
	})

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
	n, err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle})
	if err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}
	if n != 1 {
		t.Errorf("InsertPuzzles() inserted = %d, want 1", n)
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
	n, err = InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{puzzle})
	if err != nil {
		t.Fatalf("InsertPuzzles() duplicate insert error: %v", err)
	}
	if n != 0 {
		t.Errorf("InsertPuzzles() duplicate insert inserted = %d, want 0", n)
	}
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(zeroGrid)).Scan(&count); err != nil {
		t.Fatalf("query after duplicate insert: %v", err)
	}
	if count != 1 {
		t.Errorf("count after duplicate insert = %d, want 1 (ON CONFLICT DO NOTHING should prevent a second row)", count)
	}
}

// gridWithMarker returns a deterministic, distinct all-zero-except-one-cell
// grid: it sets cell 0 to a value derived from marker (1-9, cycling), giving
// callers an easy way to build many puzzles with mutually distinct givens
// for bulk-insert tests without needing valid Sudoku content (InsertPuzzles
// doesn't validate puzzle content, only stores what it's given).
func gridWithMarker(marker int) sudoku.Grid {
	// Encode marker in base 9 across the whole first row (9 cells), giving
	// 9^9 (> 387 million) distinct grids — far more than any test here
	// needs, so distinct markers never collide.
	var g sudoku.Grid
	for i := 0; i < 9; i++ {
		g[0][i] = marker%9 + 1
		marker /= 9
	}
	return g
}

func TestOpenAndInsertPuzzlesMultiChunk(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
	})

	ctx := context.Background()

	const n = insertChunkSize + 250 // spans more than one chunk
	puzzles := make([]sudoku.Puzzle, n)
	for i := range puzzles {
		g := gridWithMarker(i + 1) // +1 keeps clear of the zero-grid fixture used elsewhere
		puzzles[i] = sudoku.Puzzle{Givens: g, Solution: g, Difficulty: sudoku.Medium}
	}

	t.Cleanup(func() {
		for _, p := range puzzles {
			if _, err := sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(p.Givens)); err != nil {
				t.Logf("cleanup: %v", err)
			}
		}
	})

	inserted, err := InsertPuzzles(ctx, sqlDB, puzzles)
	if err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}
	if inserted != int64(n) {
		t.Errorf("InsertPuzzles() inserted = %d, want %d", inserted, n)
	}

	var sb strings.Builder
	sb.WriteString("SELECT count(*) FROM puzzles WHERE givens IN (")
	args := make([]any, len(puzzles))
	for i, p := range puzzles {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "$%d", i+1)
		args[i] = gridToString(p.Givens)
	}
	sb.WriteString(")")

	var count int
	if err := sqlDB.QueryRowContext(ctx, sb.String(), args...).Scan(&count); err != nil {
		t.Fatalf("query inserted rows: %v", err)
	}
	if count != n {
		t.Errorf("count = %d, want %d", count, n)
	}
}

func TestOpenAndInsertPuzzlesIntraBatchDuplicate(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() {
		sqlDB.Close()
	})

	ctx := context.Background()
	g := gridWithMarker(10000) // distinct from the fixtures used in other tests
	t.Cleanup(func() {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(g)); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	p1 := sudoku.Puzzle{Givens: g, Solution: g, Difficulty: sudoku.Hard}
	p2 := sudoku.Puzzle{Givens: g, Solution: g, Difficulty: sudoku.Hard} // same givens, same call

	inserted, err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{p1, p2})
	if err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}
	if inserted != 1 {
		t.Errorf("InsertPuzzles() inserted = %d, want 1 for an intra-batch duplicate", inserted)
	}

	var count int
	if err := sqlDB.QueryRowContext(ctx, "SELECT count(*) FROM puzzles WHERE givens = $1", gridToString(g)).Scan(&count); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}
}
