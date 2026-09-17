package db

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// testUser inserts a fresh user row and returns its id, cleaning up
// after the test (cascades to sessions/games via ON DELETE CASCADE).
func testUser(t *testing.T, sqlDB *sql.DB) int64 {
	t.Helper()
	ctx := context.Background()
	var id int64
	providerUserID := gameTestID(t)
	err := sqlDB.QueryRowContext(ctx,
		"INSERT INTO users (provider, provider_user_id, email) VALUES ('google', $1, $2) RETURNING id",
		providerUserID, providerUserID+"@example.com",
	).Scan(&id)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := sqlDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
			t.Logf("cleanup test user: %v", err)
		}
	})
	return id
}

// gameTestID returns a short random string, distinct per call, for use
// as a unique fixture key (provider_user_id here) so parallel test runs
// never collide.
func gameTestID(t *testing.T) string {
	t.Helper()
	return game.NewID()
}

func testGamePuzzle() sudoku.Puzzle {
	var givens, solution sudoku.Grid
	givens[0][0] = 5
	solution[0][0] = 5
	solution[0][1] = 3
	return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: sudoku.Easy}
}

func TestGameStoreCreateAndGet(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	g, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if g.ID == "" {
		t.Fatal("Create() returned a game with an empty ID")
	}
	if g.MaxMistakes != game.MaxMistakesFor(sudoku.Easy) {
		t.Errorf("MaxMistakes = %d, want %d", g.MaxMistakes, game.MaxMistakesFor(sudoku.Easy))
	}

	got, err := store.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if *got != *g {
		t.Errorf("Get() returned %+v, want %+v", got, g)
	}
}

func TestGameStoreGetUnknownID(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	if _, err := store.Get(context.Background(), "does-not-exist"); err != game.ErrNotFound {
		t.Errorf("Get() error = %v, want game.ErrNotFound", err)
	}
}

func TestGameStoreGetScopedToUser(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	owner := testUser(t, sqlDB)
	other := testUser(t, sqlDB)

	g, err := NewGameStore(sqlDB, owner).Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := NewGameStore(sqlDB, other).Get(ctx, g.ID); err != game.ErrNotFound {
		t.Errorf("Get() by a different user error = %v, want game.ErrNotFound", err)
	}
}

func TestGameStoreCreateResumesInProgressGame(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	first, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("first Create() error = %v, want nil", err)
	}
	second, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("second Create() error = %v, want nil", err)
	}

	if first.ID != second.ID {
		t.Errorf("second Create() for the same difficulty returned a new game %q, want the existing in-progress game %q", second.ID, first.ID)
	}
}

func TestGameStoreCreateStartsNewGameAfterCompletion(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	first, err := store.Create(ctx, testGamePuzzle()) // solution[0][1] == 3
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
	if _, err := store.ApplyMove(ctx, first.ID, 0, 1, 3); err != nil { // solves it
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}

	second, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("second Create() error = %v, want nil", err)
	}
	if second.ID == first.ID {
		t.Error("Create() after completing the previous game returned the same game, want a new one")
	}
}

// TestGameStoreCreateHandlesConcurrentInsert exercises the race between
// two concurrent Create calls for the same (user_id, difficulty): the
// games_one_active_per_difficulty unique index lets only one INSERT
// win, and the loser must resolve by returning the winner's row rather
// than propagating a raw unique-violation error. This is inherently
// timing-dependent — it is not guaranteed to hit the exact race window
// on every run — but it stands as a regression guard and, run with
// -race and -count, gives the race a real chance to occur.
func TestGameStoreCreateHandlesConcurrentInsert(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	const n = 2
	type result struct {
		g   *game.Game
		err error
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := store.Create(ctx, testGamePuzzle())
			results <- result{g, err}
		}()
	}
	wg.Wait()
	close(results)

	var ids []string
	for r := range results {
		if r.err != nil {
			t.Fatalf("Create() error = %v, want nil", r.err)
		}
		ids = append(ids, r.g.ID)
	}
	if len(ids) != n {
		t.Fatalf("got %d results, want %d", len(ids), n)
	}
	if ids[0] != ids[1] {
		t.Errorf("concurrent Create() calls for the same difficulty returned different games %q and %q, want the same game", ids[0], ids[1])
	}
}

func TestGameStoreApplyMove(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	ctx := context.Background()
	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	g, err := store.Create(ctx, testGamePuzzle())
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	updated, err := store.ApplyMove(ctx, g.ID, 0, 1, 9) // wrong: solution wants 3
	if err != nil {
		t.Fatalf("ApplyMove() error = %v, want nil", err)
	}
	if updated.Mistakes != 1 {
		t.Errorf("Mistakes = %d, want 1", updated.Mistakes)
	}
	if updated.Current[0][1] != 9 {
		t.Errorf("Current[0][1] = %d, want 9", updated.Current[0][1])
	}

	// Persisted, not just returned: a fresh Get sees the same state.
	got, err := store.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if got.Mistakes != 1 || got.Current[0][1] != 9 {
		t.Errorf("Get() after ApplyMove = %+v, want Mistakes=1, Current[0][1]=9", got)
	}
}

func TestGameStoreApplyMoveUnknownGame(t *testing.T) {
	url := testDatabaseURL(t)
	sqlDB, err := Open(url)
	if err != nil {
		t.Fatalf("Open() error: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	userID := testUser(t, sqlDB)
	store := NewGameStore(sqlDB, userID)

	if _, err := store.ApplyMove(context.Background(), "does-not-exist", 0, 1, 5); err != game.ErrNotFound {
		t.Errorf("ApplyMove() error = %v, want game.ErrNotFound", err)
	}
}

var _ game.GameStore = (*GameStore)(nil)
