package sudoku

import "testing"

func TestSolve(t *testing.T) {
	solution := mustParseGrid(t, wikipediaSolution)

	tests := []struct {
		name      string
		grid      Grid
		wantCount int
		wantSame  bool // if true, the returned solution must equal `solution`
	}{
		{
			name:      "puzzle with a unique solution",
			grid:      mustParseGrid(t, wikipediaPuzzle),
			wantCount: 1,
			wantSame:  true,
		},
		{
			name:      "already-solved grid",
			grid:      solution,
			wantCount: 1,
			wantSame:  true,
		},
		{
			name:      "grid with a single given has multiple solutions",
			grid:      mustParseGrid(t, oneClueGrid),
			wantCount: 2,
		},
		{
			name:      "grid with conflicting givens is unsolvable",
			grid:      mustParseGrid(t, conflictingGivensGrid),
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := Solve(tt.grid)
			if count != tt.wantCount {
				t.Fatalf("Solve() count = %d, want %d", count, tt.wantCount)
			}
			if tt.wantSame && got != solution {
				t.Errorf("Solve() solution =\n%v\nwant\n%v", got, solution)
			}
		})
	}
}
