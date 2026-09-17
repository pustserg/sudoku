package sudoku

import "testing"

func TestGridValid(t *testing.T) {
	tests := []struct {
		name string
		grid Grid
		want bool
	}{
		{
			name: "empty grid is valid",
			grid: Grid{},
			want: true,
		},
		{
			name: "wikipedia solved grid is valid",
			grid: mustParseGrid(t, wikipediaSolution),
			want: true,
		},
		{
			name: "wikipedia puzzle givens are valid",
			grid: mustParseGrid(t, wikipediaPuzzle),
			want: true,
		},
		{
			name: "duplicate in a row is invalid",
			grid: mustParseGrid(t, conflictingGivensGrid),
			want: false,
		},
		{
			name: "duplicate in a column is invalid",
			grid: mustParseGrid(t, `
5 . . | . . . | . . .
5 . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
`),
			want: false,
		},
		{
			name: "duplicate in a box is invalid",
			grid: mustParseGrid(t, `
5 . . | . . . | . . .
. 5 . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
`),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.grid.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCandidateMask(t *testing.T) {
	g := mustParseGrid(t, wikipediaPuzzle)

	// Cell (0,2) is empty in the wikipedia puzzle. Row 0 already has
	// 5,3,7; column 2 already has 8; box 0 already has 3,5,6,8,9. The
	// digits consistent with all three are 1, 2, and 4.
	mask := candidateMask(g, 0, 2)
	want := uint16(1)<<1 | uint16(1)<<2 | uint16(1)<<4
	if mask != want {
		t.Errorf("candidateMask(0,2) = %09b, want %09b", mask, want)
	}
	if got := candidateDigits(mask); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 4 {
		t.Errorf("candidateDigits(%09b) = %v, want [1 2 4]", mask, got)
	}
	if got := popcount(mask); got != 3 {
		t.Errorf("popcount(%09b) = %d, want 3", mask, got)
	}
}
