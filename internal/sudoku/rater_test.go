package sudoku

import "testing"

func TestRate(t *testing.T) {
	tests := []struct {
		name string
		grid Grid
		want Difficulty
	}{
		{
			name: "already-solved grid needs no techniques",
			grid: mustParseGrid(t, wikipediaSolution),
			want: Easy,
		},
		{
			name: "wikipedia example puzzle is solvable by basic scanning",
			grid: mustParseGrid(t, wikipediaPuzzle),
			want: Easy,
		},
		{
			name: "AI Escargot stalls the basic technique set",
			grid: mustParseGrid(t, aiEscargotPuzzle),
			want: Expert,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Rate(tt.grid); got != tt.want {
				t.Errorf("Rate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyNakedSingle(t *testing.T) {
	var values Grid
	var cands candidateGrid
	cands[4][4] = uint16(1) << 7 // only remaining candidate is 7

	if changed := applyNakedSingle(&values, &cands); !changed {
		t.Fatalf("applyNakedSingle() = false, want true")
	}
	if values[4][4] != 7 {
		t.Errorf("values[4][4] = %d, want 7", values[4][4])
	}
	if cands[4][4] != 0 {
		t.Errorf("cands[4][4] = %09b, want 0 after placement", cands[4][4])
	}
}

func TestApplyHiddenSingle(t *testing.T) {
	var values Grid
	var cands candidateGrid
	filler := uint16(1)<<1 | uint16(1)<<2
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			cands[r][c] = filler
		}
	}
	// Row 3 has two candidate cells for digit 6, so no hidden single
	// there. Digit 6 is otherwise absent from column 0, so scanning
	// column 0 finds a hidden single for 6 at (3,0).
	cands[3][0] = uint16(1) << 6
	cands[3][5] = uint16(1)<<6 | uint16(1)<<1

	if changed := applyHiddenSingle(&values, &cands); !changed {
		t.Fatalf("applyHiddenSingle() = false, want true")
	}
	if values[3][0] != 6 {
		t.Errorf("values[3][0] = %d, want 6", values[3][0])
	}
}

func TestApplyLockedCandidates(t *testing.T) {
	var cands candidateGrid
	bit5 := uint16(1) << 5
	// Box 0 (rows 0-2, cols 0-2): candidate 5 only appears in column 0,
	// at (0,0) and (1,0) — a pointing pair.
	cands[0][0] = bit5
	cands[1][0] = bit5
	// (5,0) is in column 0 but outside box 0, so its candidate 5 should
	// be eliminated.
	cands[5][0] = bit5 | uint16(1)<<3

	if changed := applyLockedCandidates(&cands); !changed {
		t.Fatalf("applyLockedCandidates() = false, want true")
	}
	if cands[5][0]&bit5 != 0 {
		t.Errorf("cands[5][0] = %09b, still has candidate 5", cands[5][0])
	}
	if cands[0][0] != bit5 || cands[1][0] != bit5 {
		t.Errorf("pointing-pair cells changed unexpectedly: %09b, %09b", cands[0][0], cands[1][0])
	}
}

func TestApplyNakedPair(t *testing.T) {
	var cands candidateGrid
	pair := uint16(1)<<2 | uint16(1)<<7
	// Row 0: (0,0) and (0,1) form a naked pair {2,7}.
	cands[0][0] = pair
	cands[0][1] = pair
	// (0,2) should lose 2 and 7 as candidates, keeping only 4.
	cands[0][2] = pair | uint16(1)<<4

	if changed := applyNakedPair(&cands); !changed {
		t.Fatalf("applyNakedPair() = false, want true")
	}
	want := uint16(1) << 4
	if cands[0][2] != want {
		t.Errorf("cands[0][2] = %09b, want %09b", cands[0][2], want)
	}
}

func TestApplyHiddenPair(t *testing.T) {
	var cands candidateGrid
	// Row 0: digits 3 and 8 only appear as candidates in (0,0) and (0,1),
	// which also carry other (removable) candidates.
	cands[0][0] = uint16(1)<<3 | uint16(1)<<8 | uint16(1)<<1
	cands[0][1] = uint16(1)<<3 | uint16(1)<<8 | uint16(1)<<9
	for c := 2; c < 9; c++ {
		cands[0][c] = uint16(1) << 5 // filler: no 3 or 8 here
	}

	if changed := applyHiddenPair(&cands); !changed {
		t.Fatalf("applyHiddenPair() = false, want true")
	}
	want := uint16(1)<<3 | uint16(1)<<8
	if cands[0][0] != want || cands[0][1] != want {
		t.Errorf("hidden-pair cells = %09b, %09b, want both %09b", cands[0][0], cands[0][1], want)
	}
}
