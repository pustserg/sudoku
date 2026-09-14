package sudoku

import "testing"

// mustParseGrid parses a 9x9 puzzle layout into a Grid. Digits '1'-'9' are
// filled cells; '.' or '0' are empty cells. Spaces, tabs, newlines, '|',
// '-', and '+' are ignored, so puzzles can be written as a readable grid
// with box-boundary separators. Fails the test unless exactly 81 cell
// characters are found.
func mustParseGrid(t *testing.T, s string) Grid {
	t.Helper()
	var g Grid
	i := 0
	for _, ch := range s {
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '|' || ch == '-' || ch == '+':
			continue
		case ch == '.' || ch == '0':
			if i >= 81 {
				t.Fatalf("mustParseGrid: more than 81 cell characters in %q", s)
			}
			g[i/9][i%9] = 0
			i++
		case ch >= '1' && ch <= '9':
			if i >= 81 {
				t.Fatalf("mustParseGrid: more than 81 cell characters in %q", s)
			}
			g[i/9][i%9] = int(ch - '0')
			i++
		default:
			t.Fatalf("mustParseGrid: invalid character %q", ch)
		}
	}
	if i != 81 {
		t.Fatalf("mustParseGrid: found %d cell characters, want 81", i)
	}
	return g
}

// wikipediaPuzzle and wikipediaSolution are the standard example puzzle
// and solution from Wikipedia's Sudoku article: a well-known, widely
// reproduced puzzle solvable by basic scanning (naked/hidden singles)
// alone, with a single unique solution.
const wikipediaPuzzle = `
5 3 . | . 7 . | . . .
6 . . | 1 9 5 | . . .
. 9 8 | . . . | . 6 .
------+-------+------
8 . . | . 6 . | . . 3
4 . . | 8 . 3 | . . 1
7 . . | . 2 . | . . 6
------+-------+------
. 6 . | . . . | 2 8 .
. . . | 4 1 9 | . . 5
. . . | . 8 . | . 7 9
`

const wikipediaSolution = `
5 3 4 | 6 7 8 | 9 1 2
6 7 2 | 1 9 5 | 3 4 8
1 9 8 | 3 4 2 | 5 6 7
------+-------+------
8 5 9 | 7 6 1 | 4 2 3
4 2 6 | 8 5 3 | 7 9 1
7 1 3 | 9 2 4 | 8 5 6
------+-------+------
9 6 1 | 5 3 7 | 2 8 4
2 8 7 | 4 1 9 | 6 3 5
3 4 5 | 2 8 6 | 1 7 9
`

// oneClueGrid has a single given digit and is deliberately far from a
// real puzzle: it has many valid completions, used to test the solver's
// "multiple solutions" path.
const oneClueGrid = `
5 . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
`

// conflictingGivensGrid has two identical givens in row 0, making it
// invalid and therefore unsolvable.
const conflictingGivensGrid = `
5 5 . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
------+-------+------
. . . | . . . | . . .
. . . | . . . | . . .
. . . | . . . | . . .
`

// aiEscargotPuzzle is "AI Escargot", a puzzle constructed by Arto Inkala
// and widely documented as one of the hardest known Sudoku puzzles — it
// requires solving techniques well beyond basic scanning.
const aiEscargotPuzzle = `
1 . . | . . 7 | . 9 .
. 3 . | . 2 . | . . 8
. . 9 | 6 . . | 5 . .
------+-------+------
. . 5 | 3 . . | 9 . .
. 1 . | . 8 . | . . 2
6 . . | . . 4 | . . .
------+-------+------
3 . . | . . . | . 1 .
. 4 . | . . . | . . 7
. . 7 | . . . | 3 . .
`
