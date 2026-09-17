package sudoku

// Solve attempts to solve g. It returns the first solution found (the
// zero Grid if none exists) and the number of distinct solutions found,
// capped at 2 — callers only ever need to distinguish "none", "unique",
// or "multiple". A g that is already invalid (see Grid.Valid) has no
// solutions by definition and short-circuits to (Grid{}, 0).
func Solve(g Grid) (solution Grid, count int) {
	if !g.Valid() {
		return Grid{}, 0
	}
	solve(&g, &solution, &count)
	return solution, count
}

// solve performs recursive backtracking on g in place, recording the
// first complete grid it finds into *solution and incrementing *count for
// each distinct solution, stopping the search once *count reaches 2.
func solve(g *Grid, solution *Grid, count *int) {
	row, col, mask, ok := mostConstrainedCell(*g)
	if !ok {
		// No empty cells left: g is a complete solution.
		*count++
		if *count == 1 {
			*solution = *g
		}
		return
	}
	if mask == 0 {
		return // dead end: an empty cell with no legal digit
	}
	for _, d := range candidateDigits(mask) {
		g[row][col] = d
		solve(g, solution, count)
		g[row][col] = 0
		if *count >= 2 {
			return
		}
	}
}

// mostConstrainedCell finds the empty cell in g with the fewest legal
// candidates (the most-constrained-cell heuristic, which keeps the
// backtracking search's branching factor low). ok is false only when g
// has no empty cells at all. If the returned mask is 0, that cell is a
// dead end (an empty cell with no legal digit).
func mostConstrainedCell(g Grid) (row, col int, mask uint16, ok bool) {
	best := 10
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			if g[r][c] != 0 {
				continue
			}
			m := candidateMask(g, r, c)
			n := popcount(m)
			ok = true
			if n < best {
				best, row, col, mask = n, r, c, m
			}
			if n == 0 {
				return row, col, mask, ok
			}
		}
	}
	return row, col, mask, ok
}
