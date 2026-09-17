package db

import (
	"context"
	"database/sql"
	"fmt"
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

	// Two distinct Easy puzzles so we can confirm RandomPuzzle actually
	// varies which one it returns across calls (not proof of true
	// randomness, just that it isn't hardcoded to always return the
	// same row).
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
	if len(seen) < 2 {
		t.Fatalf("RandomPuzzle() returned only %d distinct puzzle(s) across 20 calls, want both fixtures represented (got %v)", len(seen), seen)
	}
}

func TestRandomPuzzleSetsID(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	var grid sudoku.Grid
	grid[0][0] = 4
	fixture := sudoku.Puzzle{Givens: grid, Solution: grid, Difficulty: sudoku.Medium}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(fixture.Givens))
	})
	if _, err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{fixture}); err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}

	var wantID int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM puzzles WHERE givens = $1", gridToString(fixture.Givens)).Scan(&wantID); err != nil {
		t.Fatalf("query fixture id: %v", err)
	}

	p, err := RandomPuzzle(ctx, sqlDB, sudoku.Medium)
	if err != nil {
		t.Fatalf("RandomPuzzle() error: %v", err)
	}
	if p.ID == 0 {
		t.Error("RandomPuzzle() returned a puzzle with ID = 0, want the row's real id")
	}
}

func TestRandomPuzzleForUserExcludesAlreadyPlayed(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()

	var gridA sudoku.Grid
	gridA[0][0] = 5
	fixture := sudoku.Puzzle{Givens: gridA, Solution: gridA, Difficulty: sudoku.Hard}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(fixture.Givens))
	})
	if _, err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{fixture}); err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}

	var idA int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM puzzles WHERE givens = $1", gridToString(gridA)).Scan(&idA); err != nil {
		t.Fatalf("query fixture id: %v", err)
	}

	var userID int64
	if err := sqlDB.QueryRowContext(ctx, `
		INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $1) RETURNING id`,
		"random-puzzle-for-user-test").Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	})

	// Record that this user already played puzzle A (as a completed game),
	// so RandomPuzzleForUser must never offer it again.
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status, puzzle_id)
		VALUES ('random-puzzle-for-user-test-game', $1, $2, $3, $3, $3, 0, 3, 'solved', $4)`,
		userID, int(sudoku.Hard), gridToString(gridA), idA); err != nil {
		t.Fatalf("insert played-game fixture: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM games WHERE id = 'random-puzzle-for-user-test-game'")
	})

	// Only assert idA is never offered — this database may already hold
	// other Hard puzzles from other tests/genpuzzles runs, so we can't
	// assert the result is any particular other id, only that the
	// excluded one never comes back.
	for i := 0; i < 10; i++ {
		p, err := RandomPuzzleForUser(ctx, sqlDB, sudoku.Hard, userID)
		if err != nil {
			t.Fatalf("RandomPuzzleForUser() error: %v", err)
		}
		if p.ID == idA {
			t.Fatalf("RandomPuzzleForUser() returned already-played puzzle %d, want it excluded", idA)
		}
	}
}

func TestRandomPuzzleForUserFallsBackWhenExhausted(t *testing.T) {
	url := testDatabaseURL(t)

	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", url, err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()

	var grid sudoku.Grid
	grid[0][0] = 7
	fixture := sudoku.Puzzle{Givens: grid, Solution: grid, Difficulty: sudoku.Expert}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM puzzles WHERE givens = $1", gridToString(fixture.Givens))
	})
	if _, err := InsertPuzzles(ctx, sqlDB, []sudoku.Puzzle{fixture}); err != nil {
		t.Fatalf("InsertPuzzles() error: %v", err)
	}
	var puzzleID int64
	if err := sqlDB.QueryRowContext(ctx, "SELECT id FROM puzzles WHERE givens = $1", gridToString(fixture.Givens)).Scan(&puzzleID); err != nil {
		t.Fatalf("query fixture id: %v", err)
	}

	var userID int64
	if err := sqlDB.QueryRowContext(ctx, `
		INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $1) RETURNING id`,
		"random-puzzle-exhausted-test").Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
	})
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM games WHERE user_id = $1", userID)
	})

	// Mark EVERY Expert puzzle that exists — this fixture plus whatever
	// else is already in the pool from other tests/genpuzzles runs — as
	// already played by this user, so the exclusion query genuinely has
	// nothing left to offer and must exercise the fallback.
	allExpertIDs, err := allPuzzleIDs(ctx, sqlDB, sudoku.Expert)
	if err != nil {
		t.Fatalf("query all Expert puzzle ids: %v", err)
	}
	if len(allExpertIDs) == 0 {
		t.Fatal("no Expert puzzles found even after inserting the fixture")
	}
	for i, id := range allExpertIDs {
		if _, err := sqlDB.ExecContext(ctx, `
			INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status, puzzle_id)
			VALUES ($1, $2, $3, $4, $4, $4, 0, 3, 'solved', $5)`,
			fmt.Sprintf("random-puzzle-exhausted-test-game-%d", i), userID, int(sudoku.Expert), gridToString(grid), id); err != nil {
			t.Fatalf("mark puzzle %d as played: %v", id, err)
		}
	}

	p, err := RandomPuzzleForUser(ctx, sqlDB, sudoku.Expert, userID)
	if err != nil {
		t.Fatalf("RandomPuzzleForUser() error = %v, want a fallback repeat instead of an error", err)
	}
	found := false
	for _, id := range allExpertIDs {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RandomPuzzleForUser() ID = %d, want one of the already-played Expert puzzles %v (a repeat, per the fallback)", p.ID, allExpertIDs)
	}
}

// allPuzzleIDs returns the ids of every puzzle of difficulty d currently
// in the puzzles table.
func allPuzzleIDs(ctx context.Context, sqlDB *sql.DB, d sudoku.Difficulty) ([]int64, error) {
	rows, err := sqlDB.QueryContext(ctx, "SELECT id FROM puzzles WHERE difficulty = $1", int(d))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
