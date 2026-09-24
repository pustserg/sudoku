package db

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// insertFinishedGame inserts a completed games row directly (bypassing
// real play, whose target digits these tests don't need to know) for
// use as a stats/history fixture.
func insertFinishedGame(t *testing.T, sqlDB *sql.DB, id string, userID int64, d sudoku.Difficulty, status string, mistakes int) {
	t.Helper()
	ctx := context.Background()
	var grid sudoku.Grid
	if _, err := sqlDB.ExecContext(ctx, `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status)
		VALUES ($1, $2, $3, $4, $4, $4, $5, 5, $6)`,
		id, userID, int(d), gridToString(grid), mistakes, status); err != nil {
		t.Fatalf("insert finished game fixture: %v", err)
	}
	t.Cleanup(func() {
		sqlDB.ExecContext(ctx, "DELETE FROM games WHERE id = $1", id)
	})
}

func TestUserStatsReturnsAllFourDifficultiesWithZeros(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)

	stats, err := UserStats(context.Background(), sqlDB, userID)
	if err != nil {
		t.Fatalf("UserStats() error: %v", err)
	}
	if len(stats) != 4 {
		t.Fatalf("UserStats() returned %d rows, want 4 (one per difficulty)", len(stats))
	}
	want := []sudoku.Difficulty{sudoku.Easy, sudoku.Medium, sudoku.Hard, sudoku.Expert}
	for i, d := range want {
		if stats[i].Difficulty != d {
			t.Errorf("stats[%d].Difficulty = %v, want %v", i, stats[i].Difficulty, d)
		}
		if stats[i].Played != 0 || stats[i].Solved != 0 || stats[i].Failed != 0 {
			t.Errorf("stats[%d] = %+v, want all-zero counts for a user with no games", i, stats[i])
		}
		if stats[i].BestMistakes != nil {
			t.Errorf("stats[%d].BestMistakes = %v, want nil (never solved)", i, *stats[i].BestMistakes)
		}
	}
}

func TestUserStatsAggregatesPlayedGames(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)
	insertFinishedGame(t, sqlDB, "stats-test-1", userID, sudoku.Medium, "solved", 2)
	insertFinishedGame(t, sqlDB, "stats-test-2", userID, sudoku.Medium, "solved", 0)
	insertFinishedGame(t, sqlDB, "stats-test-3", userID, sudoku.Medium, "failed", 5)

	stats, err := UserStats(context.Background(), sqlDB, userID)
	if err != nil {
		t.Fatalf("UserStats() error: %v", err)
	}

	var medium DifficultyStats
	found := false
	for _, s := range stats {
		if s.Difficulty == sudoku.Medium {
			medium = s
			found = true
		}
	}
	if !found {
		t.Fatal("UserStats() has no Medium row")
	}

	if medium.Played != 3 {
		t.Errorf("Played = %d, want 3", medium.Played)
	}
	if medium.Solved != 2 {
		t.Errorf("Solved = %d, want 2", medium.Solved)
	}
	if medium.Failed != 1 {
		t.Errorf("Failed = %d, want 1", medium.Failed)
	}
	wantAvg := (2.0 + 0.0 + 5.0) / 3.0
	if diff := medium.AvgMistakes - wantAvg; diff > 0.001 || diff < -0.001 {
		t.Errorf("AvgMistakes = %v, want %v", medium.AvgMistakes, wantAvg)
	}
	if medium.BestMistakes == nil || *medium.BestMistakes != 0 {
		t.Errorf("BestMistakes = %v, want 0 (the better of the two solved games)", medium.BestMistakes)
	}
}

func TestUserStatsIgnoresInProgressGames(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	if _, err := NewGameStore(sqlDB, userID).Create(ctx, sudoku.Puzzle{Difficulty: sudoku.Hard}); err != nil {
		t.Fatalf("create in-progress game: %v", err)
	}

	stats, err := UserStats(ctx, sqlDB, userID)
	if err != nil {
		t.Fatalf("UserStats() error: %v", err)
	}
	for _, s := range stats {
		if s.Difficulty == sudoku.Hard && s.Played != 0 {
			t.Errorf("Hard.Played = %d, want 0 (the only game is still in_progress)", s.Played)
		}
	}
}

func TestUserStatsScopedToUser(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	owner := testUser(t, sqlDB)
	other := testUser(t, sqlDB)
	insertFinishedGame(t, sqlDB, "stats-scope-test", owner, sudoku.Expert, "solved", 1)

	stats, err := UserStats(context.Background(), sqlDB, other)
	if err != nil {
		t.Fatalf("UserStats() error: %v", err)
	}
	for _, s := range stats {
		if s.Difficulty == sudoku.Expert && s.Played != 0 {
			t.Errorf("a different user's Expert.Played = %d, want 0", s.Played)
		}
	}
}

func TestRecentGamesOrderedNewestFirstAndScoped(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	insertFinishedGame(t, sqlDB, "recent-test-1", userID, sudoku.Easy, "solved", 1)
	if _, err := sqlDB.ExecContext(ctx, "UPDATE games SET updated_at = now() - interval '2 hours' WHERE id = 'recent-test-1'"); err != nil {
		t.Fatalf("backdate recent-test-1: %v", err)
	}
	insertFinishedGame(t, sqlDB, "recent-test-2", userID, sudoku.Hard, "failed", 3)

	other := testUser(t, sqlDB)
	insertFinishedGame(t, sqlDB, "recent-test-other", other, sudoku.Expert, "solved", 0)

	games, err := RecentGames(ctx, sqlDB, userID, 20)
	if err != nil {
		t.Fatalf("RecentGames() error: %v", err)
	}
	if len(games) != 2 {
		t.Fatalf("RecentGames() returned %d games, want 2 (scoped to this user)", len(games))
	}
	if games[0].ID != "recent-test-2" || games[1].ID != "recent-test-1" {
		t.Errorf("RecentGames() order = [%s, %s], want [recent-test-2, recent-test-1] (newest first)", games[0].ID, games[1].ID)
	}
}

func TestRecentGamesExcludesInProgress(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	if _, err := NewGameStore(sqlDB, userID).Create(ctx, sudoku.Puzzle{Difficulty: sudoku.Easy}); err != nil {
		t.Fatalf("create in-progress game: %v", err)
	}

	games, err := RecentGames(ctx, sqlDB, userID, 20)
	if err != nil {
		t.Fatalf("RecentGames() error: %v", err)
	}
	if len(games) != 0 {
		t.Errorf("RecentGames() returned %d games, want 0 (the only game is still in_progress)", len(games))
	}
}

func TestRecentGamesRespectsLimit(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	for i := 0; i < 5; i++ {
		insertFinishedGame(t, sqlDB, gameTestID(t), userID, sudoku.Easy, "solved", i)
	}

	games, err := RecentGames(ctx, sqlDB, userID, 3)
	if err != nil {
		t.Fatalf("RecentGames() error: %v", err)
	}
	if len(games) != 3 {
		t.Errorf("RecentGames() with limit 3 returned %d games, want 3", len(games))
	}
}
