package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// insertChunkSize is the number of puzzles per multi-row INSERT
// statement within InsertPuzzles' transaction.
const insertChunkSize = 500

// InsertPuzzles inserts puzzles into the puzzles table inside a single
// transaction, batched into chunks of insertChunkSize rows per
// statement. A puzzle whose givens already exist in the table is
// silently skipped (ON CONFLICT DO NOTHING) rather than failing the
// whole call. It returns the number of rows actually inserted, which
// may be less than len(puzzles) when some givens already existed.
func InsertPuzzles(ctx context.Context, sqlDB *sql.DB, puzzles []sudoku.Puzzle) (inserted int64, err error) {
	if len(puzzles) == 0 {
		return 0, nil
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op once Commit succeeds

	for _, chunk := range chunkPuzzles(puzzles, insertChunkSize) {
		n, err := insertChunk(ctx, tx, chunk)
		if err != nil {
			return inserted, fmt.Errorf("insert chunk: %w", err)
		}
		inserted += n
	}

	if err := tx.Commit(); err != nil {
		return inserted, fmt.Errorf("commit transaction: %w", err)
	}
	return inserted, nil
}

// chunkPuzzles splits puzzles into consecutive slices of at most size
// elements each.
func chunkPuzzles(puzzles []sudoku.Puzzle, size int) [][]sudoku.Puzzle {
	var chunks [][]sudoku.Puzzle
	for size < len(puzzles) {
		puzzles, chunks = puzzles[size:], append(chunks, puzzles[:size:size])
	}
	if len(puzzles) > 0 {
		chunks = append(chunks, puzzles)
	}
	return chunks
}

// insertChunk inserts one chunk of puzzles via a single multi-row
// INSERT statement and returns the number of rows actually inserted.
func insertChunk(ctx context.Context, tx *sql.Tx, chunk []sudoku.Puzzle) (int64, error) {
	var sb strings.Builder
	sb.WriteString("INSERT INTO puzzles (givens, solution, difficulty) VALUES ")
	args := make([]any, 0, len(chunk)*3)
	for i, p := range chunk {
		if i > 0 {
			sb.WriteString(", ")
		}
		n := i * 3
		fmt.Fprintf(&sb, "($%d, $%d, $%d)", n+1, n+2, n+3)
		args = append(args, gridToString(p.Givens), gridToString(p.Solution), int(p.Difficulty))
	}
	sb.WriteString(" ON CONFLICT (givens) DO NOTHING")

	res, err := tx.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
