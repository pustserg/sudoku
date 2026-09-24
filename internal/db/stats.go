package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// allDifficulties is the fixed display order for per-difficulty stats.
var allDifficulties = []sudoku.Difficulty{sudoku.Easy, sudoku.Medium, sudoku.Hard, sudoku.Expert}

// DifficultyStats summarizes one user's finished (solved or failed)
// games at one difficulty. In-progress games are never counted.
type DifficultyStats struct {
	Difficulty   sudoku.Difficulty
	Played       int // Solved + Failed
	Solved       int
	Failed       int
	AvgMistakes  float64 // over all Played games; 0 if Played == 0
	BestMistakes *int    // fewest mistakes in any Solved game; nil if Solved == 0
}

// UserStats returns one DifficultyStats per difficulty (Easy, Medium,
// Hard, Expert, always in that order, even for a difficulty the user
// has never played) summarizing userID's finished games.
func UserStats(ctx context.Context, sqlDB *sql.DB, userID int64) ([]DifficultyStats, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT difficulty,
		       count(*) FILTER (WHERE status IN ('solved', 'failed')) AS played,
		       count(*) FILTER (WHERE status = 'solved') AS solved,
		       count(*) FILTER (WHERE status = 'failed') AS failed,
		       COALESCE(AVG(mistakes) FILTER (WHERE status IN ('solved', 'failed')), 0) AS avg_mistakes,
		       MIN(mistakes) FILTER (WHERE status = 'solved') AS best_mistakes
		FROM games
		WHERE user_id = $1
		GROUP BY difficulty`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user stats: %w", err)
	}
	defer rows.Close()

	byDifficulty := make(map[sudoku.Difficulty]DifficultyStats)
	for rows.Next() {
		var difficulty int
		var s DifficultyStats
		var best sql.NullInt64
		if err := rows.Scan(&difficulty, &s.Played, &s.Solved, &s.Failed, &s.AvgMistakes, &best); err != nil {
			return nil, fmt.Errorf("scan user stats row: %w", err)
		}
		s.Difficulty = sudoku.Difficulty(difficulty)
		if best.Valid {
			v := int(best.Int64)
			s.BestMistakes = &v
		}
		byDifficulty[s.Difficulty] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read user stats rows: %w", err)
	}

	stats := make([]DifficultyStats, len(allDifficulties))
	for i, d := range allDifficulties {
		if s, ok := byDifficulty[d]; ok {
			stats[i] = s
		} else {
			stats[i] = DifficultyStats{Difficulty: d}
		}
	}
	return stats, nil
}

// GameSummary is one finished game as shown in a user's history list.
type GameSummary struct {
	ID         string
	Difficulty sudoku.Difficulty
	Status     string // "solved" or "failed"
	Mistakes   int
	UpdatedAt  time.Time
}

// RecentGames returns userID's most recently finished (solved or
// failed) games, newest first, up to limit. In-progress games are
// never included.
func RecentGames(ctx context.Context, sqlDB *sql.DB, userID int64, limit int) ([]GameSummary, error) {
	rows, err := sqlDB.QueryContext(ctx, `
		SELECT id, difficulty, status, mistakes, updated_at
		FROM games
		WHERE user_id = $1 AND status IN ('solved', 'failed')
		ORDER BY updated_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent games: %w", err)
	}
	defer rows.Close()

	var games []GameSummary
	for rows.Next() {
		var g GameSummary
		var difficulty int
		if err := rows.Scan(&g.ID, &difficulty, &g.Status, &g.Mistakes, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan recent game row: %w", err)
		}
		g.Difficulty = sudoku.Difficulty(difficulty)
		games = append(games, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recent games rows: %w", err)
	}
	return games, nil
}
