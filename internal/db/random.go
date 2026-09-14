package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// RandomPuzzle returns one randomly-selected puzzle of difficulty d from
// the puzzles table.
func RandomPuzzle(ctx context.Context, sqlDB *sql.DB, d sudoku.Difficulty) (sudoku.Puzzle, error) {
	row := sqlDB.QueryRowContext(ctx,
		"SELECT givens, solution, difficulty FROM puzzles WHERE difficulty = $1 ORDER BY random() LIMIT 1",
		int(d),
	)

	var givensStr, solutionStr string
	var difficulty int
	if err := row.Scan(&givensStr, &solutionStr, &difficulty); err != nil {
		if err == sql.ErrNoRows {
			return sudoku.Puzzle{}, fmt.Errorf("no puzzles of difficulty %d available: run cmd/genpuzzles to generate the pool: %w", int(d), err)
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
		Givens:     givens,
		Solution:   solution,
		Difficulty: sudoku.Difficulty(difficulty),
	}, nil
}
