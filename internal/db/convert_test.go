package db

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

// validGivens is the standard Wikipedia example puzzle's givens, flattened
// to this package's storage format ('0' for empty).
const validGivens = "530070000600195000098000060800060003400803001700020006060000280000419005000080079"

func TestGridToString(t *testing.T) {
	var g sudoku.Grid
	g[0][0] = 5
	g[0][1] = 3
	g[8][8] = 9

	got := gridToString(g)
	if len(got) != 81 {
		t.Fatalf("len(gridToString(g)) = %d, want 81", len(got))
	}
	if got[0] != '5' || got[1] != '3' {
		t.Errorf("gridToString(g)[0:2] = %q, want \"53\"", got[0:2])
	}
	if got[80] != '9' {
		t.Errorf("gridToString(g)[80] = %q, want '9'", got[80])
	}
	if got[2] != '0' {
		t.Errorf("gridToString(g)[2] = %q, want '0' for an empty cell", got[2])
	}
}

func TestStringToGrid(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "round-trips a grid with mixed empty and filled cells",
			input: validGivens,
		},
		{
			name:    "wrong length is an error",
			input:   "123",
			wantErr: true,
		},
		{
			name:    "invalid character is an error",
			input:   validGivens[:80] + "x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := stringToGrid(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("stringToGrid(%q) = nil error, want an error", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("stringToGrid(%q) unexpected error: %v", tt.input, err)
			}
			roundTripped := gridToString(g)
			if roundTripped != tt.input {
				t.Errorf("round-trip mismatch: got %q, want %q", roundTripped, tt.input)
			}
		})
	}
}
