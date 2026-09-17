// Package db contains PostgreSQL access via database/sql, no ORM.
package db

import (
	"fmt"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// gridToString flattens g into an 81-character string, row-major, '0'
// for empty cells and '1'-'9' for filled ones — the layout stored in the
// puzzles table's givens/solution columns.
func gridToString(g sudoku.Grid) string {
	b := make([]byte, 0, 81)
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			b = append(b, byte('0'+g[r][c]))
		}
	}
	return string(b)
}

// stringToGrid parses an 81-character digit string (as produced by
// gridToString) back into a Grid. It returns an error if s is not exactly
// 81 characters, each '0'-'9'.
func stringToGrid(s string) (sudoku.Grid, error) {
	var g sudoku.Grid
	if len(s) != 81 {
		return g, fmt.Errorf("stringToGrid: want 81 characters, got %d", len(s))
	}
	for i := 0; i < 81; i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return sudoku.Grid{}, fmt.Errorf("stringToGrid: invalid character %q at position %d", c, i)
		}
		g[i/9][i%9] = int(c - '0')
	}
	return g, nil
}
