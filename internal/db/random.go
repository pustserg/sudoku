package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// RandomPuzzle returns one randomly-selected puzzle of difficulty d from
// the puzzles table.
func RandomPuzzle(ctx context.Context, sqlDB *sql.DB, d sudoku.Difficulty) (sudoku.Puzzle, error) {
	row := sqlDB.QueryRowContext(ctx,
		"SELECT id, givens, solution, difficulty FROM puzzles WHERE difficulty = $1 ORDER BY random() LIMIT 1",
		int(d),
	)
	return scanPuzzle(row, d)
}

// RandomPuzzleForUser returns one randomly-selected puzzle of difficulty
// d that userID has not already been given (tracked via games.puzzle_id
// on that user's past games at this difficulty), so a logged-in player
// doesn't see the same puzzle twice. If every puzzle of this difficulty
// has already been played by userID, it falls back to RandomPuzzle (a
// repeat) rather than erroring — some repetition beats refusing to serve
// a puzzle at all, especially with a small local pool.
func RandomPuzzleForUser(ctx context.Context, sqlDB *sql.DB, d sudoku.Difficulty, userID int64) (sudoku.Puzzle, error) {
	row := sqlDB.QueryRowContext(ctx, `
		SELECT id, givens, solution, difficulty FROM puzzles
		WHERE difficulty = $1
		  AND id NOT IN (SELECT puzzle_id FROM games WHERE user_id = $2 AND puzzle_id IS NOT NULL)
		ORDER BY random() LIMIT 1`,
		int(d), userID,
	)
	p, err := scanPuzzle(row, d)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RandomPuzzle(ctx, sqlDB, d)
		}
		return sudoku.Puzzle{}, err
	}
	return p, nil
}

// scanPuzzle scans a single puzzles row (id, givens, solution,
// difficulty, in that order, as both RandomPuzzle and
// RandomPuzzleForUser select) into a sudoku.Puzzle. d is used only for
// the "none available" error message.
func scanPuzzle(row *sql.Row, d sudoku.Difficulty) (sudoku.Puzzle, error) {
	var id int64
	var givensStr, solutionStr string
	var difficulty int
	if err := row.Scan(&id, &givensStr, &solutionStr, &difficulty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sudoku.Puzzle{}, fmt.Errorf("no puzzles of difficulty %d available: run cmd/genpuzzles to generate the pool: %w", int(d), sql.ErrNoRows)
		}
		return sudoku.Puzzle{}, fmt.Errorf("query random puzzle: %w", err)
	}

	givens, err := stringToGrid(givensStr)
	if err != nil {
		return sudoku.Puzzle{}, fmt.Errorf("parse givens: %w", err)
	}
	solution, err := stringToGrid(solutionStr)
	if err != nil {
		return sudoku.Puzzle{}, fmt.Errorf("parse solution: %w", err)
	}

	return sudoku.Puzzle{
		ID:         id,
		Givens:     givens,
		Solution:   solution,
		Difficulty: sudoku.Difficulty(difficulty),
	}, nil
}
