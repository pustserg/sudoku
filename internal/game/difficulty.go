package game

import "github.com/pustserg/sudoku/internal/sudoku"

// DifficultyName returns the lowercase display/wire name for d ("easy",
// "medium", "hard", "expert"), or "" if d isn't one of those four.
func DifficultyName(d sudoku.Difficulty) string {
	switch d {
	case sudoku.Easy:
		return "easy"
	case sudoku.Medium:
		return "medium"
	case sudoku.Hard:
		return "hard"
	case sudoku.Expert:
		return "expert"
	default:
		return ""
	}
}

// ParseDifficulty parses a name produced by DifficultyName back into a
// Difficulty. ok is false for any other string.
func ParseDifficulty(name string) (d sudoku.Difficulty, ok bool) {
	switch name {
	case "easy":
		return sudoku.Easy, true
	case "medium":
		return sudoku.Medium, true
	case "hard":
		return sudoku.Hard, true
	case "expert":
		return sudoku.Expert, true
	default:
		return 0, false
	}
}
