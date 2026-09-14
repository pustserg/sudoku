package game

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestDifficultyName(t *testing.T) {
	tests := []struct {
		d    sudoku.Difficulty
		want string
	}{
		{sudoku.Easy, "easy"},
		{sudoku.Medium, "medium"},
		{sudoku.Hard, "hard"},
		{sudoku.Expert, "expert"},
	}
	for _, tt := range tests {
		if got := DifficultyName(tt.d); got != tt.want {
			t.Errorf("DifficultyName(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestParseDifficulty(t *testing.T) {
	tests := []struct {
		name   string
		want   sudoku.Difficulty
		wantOK bool
	}{
		{"easy", sudoku.Easy, true},
		{"medium", sudoku.Medium, true},
		{"hard", sudoku.Hard, true},
		{"expert", sudoku.Expert, true},
		{"nightmare", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		got, ok := ParseDifficulty(tt.name)
		if ok != tt.wantOK {
			t.Errorf("ParseDifficulty(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			continue
		}
		if ok && got != tt.want {
			t.Errorf("ParseDifficulty(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
