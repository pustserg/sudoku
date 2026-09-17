package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// GameStore is a game.GameStore backed by the games table, scoped to one
// user. Used for logged-in play; anonymous play uses game.Store instead.
type GameStore struct {
	db     *sql.DB
	userID int64
}

// NewGameStore returns a GameStore for the given user.
func NewGameStore(sqlDB *sql.DB, userID int64) *GameStore {
	return &GameStore{db: sqlDB, userID: userID}
}

// Create returns the user's existing in_progress game for p.Difficulty,
// if any (implementing "resume instead of restart"), otherwise inserts
// and returns a new one.
func (s *GameStore) Create(ctx context.Context, p sudoku.Puzzle) (*game.Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	existing, err := scanGame(tx.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE user_id = $1 AND difficulty = $2 AND status = 'in_progress'`,
		s.userID, int(p.Difficulty)))
	if err == nil {
		return existing, nil // no writes made; safe to let defer roll back
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("query existing game: %w", err)
	}

	g := &game.Game{
		ID:          game.NewID(),
		Givens:      p.Givens,
		Current:     p.Givens,
		Solution:    p.Solution,
		Difficulty:  p.Difficulty,
		MaxMistakes: game.MaxMistakesFor(p.Difficulty),
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO games (id, user_id, difficulty, givens, current, solution, mistakes, max_mistakes, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'in_progress')`,
		g.ID, s.userID, int(g.Difficulty), gridToString(g.Givens), gridToString(g.Current), gridToString(g.Solution), g.Mistakes, g.MaxMistakes)
	if err != nil {
		if isOneActivePerDifficultyViolation(err) {
			// Lost a race against a concurrent Create for the same
			// (user_id, difficulty): the failed insert's transaction
			// must be rolled back before it's safe to query again.
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				return nil, fmt.Errorf("rollback after insert conflict: %w", rbErr)
			}
			return s.getInProgressGame(ctx, p.Difficulty)
		}
		return nil, fmt.Errorf("insert game: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return g, nil
}

// getInProgressGame returns the user's existing in_progress game for the
// given difficulty. Used by Create to recover the winning row after
// losing a race against a concurrent Create for the same
// (user_id, difficulty), once the failed insert's transaction has been
// rolled back.
func (s *GameStore) getInProgressGame(ctx context.Context, difficulty sudoku.Difficulty) (*game.Game, error) {
	g, err := scanGame(s.db.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE user_id = $1 AND difficulty = $2 AND status = 'in_progress'`,
		s.userID, int(difficulty)))
	if err != nil {
		return nil, fmt.Errorf("query existing game after insert conflict: %w", err)
	}
	return g, nil
}

// isOneActivePerDifficultyViolation reports whether err is a Postgres
// unique-violation on the games_one_active_per_difficulty index, meaning
// a concurrent Create for the same (user_id, difficulty) won the race.
func isOneActivePerDifficultyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "games_one_active_per_difficulty"
}

// Get returns the user's game with the given id, or game.ErrNotFound if
// it doesn't exist (including if it belongs to a different user).
func (s *GameStore) Get(ctx context.Context, id string) (*game.Game, error) {
	g, err := scanGame(s.db.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE id = $1 AND user_id = $2`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, game.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query game: %w", err)
	}
	return g, nil
}

// ApplyMove loads the user's game with the given id, applies the move
// via the shared game.ApplyMove rules, persists the result, and returns
// the updated Game.
func (s *GameStore) ApplyMove(ctx context.Context, id string, row, col, value int) (*game.Game, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	g, err := scanGame(tx.QueryRowContext(ctx, `
		SELECT id, givens, current, solution, difficulty, mistakes, max_mistakes
		FROM games WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, s.userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, game.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query game for update: %w", err)
	}

	if err := game.ApplyMove(g, row, col, value); err != nil {
		return nil, err
	}

	status := "in_progress"
	switch {
	case g.Solved():
		status = "solved"
	case g.Failed():
		status = "failed"
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE games SET current = $1, mistakes = $2, status = $3, updated_at = now() WHERE id = $4`,
		gridToString(g.Current), g.Mistakes, status, id)
	if err != nil {
		return nil, fmt.Errorf("update game: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return g, nil
}

// scanGame scans a single games row (as selected by the queries above,
// which all select the same six columns in the same order) into a
// *game.Game.
func scanGame(row *sql.Row) (*game.Game, error) {
	var g game.Game
	var givensStr, currentStr, solutionStr string
	var difficulty int
	if err := row.Scan(&g.ID, &givensStr, &currentStr, &solutionStr, &difficulty, &g.Mistakes, &g.MaxMistakes); err != nil {
		return nil, err
	}
	givens, err := stringToGrid(givensStr)
	if err != nil {
		return nil, fmt.Errorf("parse givens: %w", err)
	}
	current, err := stringToGrid(currentStr)
	if err != nil {
		return nil, fmt.Errorf("parse current: %w", err)
	}
	solution, err := stringToGrid(solutionStr)
	if err != nil {
		return nil, fmt.Errorf("parse solution: %w", err)
	}
	g.Givens, g.Current, g.Solution = givens, current, solution
	g.Difficulty = sudoku.Difficulty(difficulty)
	return &g, nil
}
