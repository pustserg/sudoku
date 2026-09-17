package sudoku

import "math/rand/v2"

// Puzzle is a generated game: the givens shown to the player, the unique
// solution, and the puzzle's rated difficulty.
type Puzzle struct {
	Givens     Grid
	Solution   Grid
	Difficulty Difficulty
}

// maxGenerateAttempts bounds how many full grids Generate tries before
// giving up on hitting the exact requested difficulty and returning its
// closest attempt instead.
const maxGenerateAttempts = 20

// Generate produces a puzzle with a unique solution, aiming for the
// requested difficulty d. If it can't hit d exactly within a bounded
// number of attempts, it returns the closest puzzle it produced, with its
// actual rated Difficulty (which may differ from d) — callers should
// bucket puzzles by the returned Difficulty, not assume d was hit.
func Generate(d Difficulty, rng *rand.Rand) Puzzle {
	var best Puzzle
	haveBest := false
	for attempt := 0; attempt < maxGenerateAttempts; attempt++ {
		solution := generateSolvedGrid(rng)
		givens := digHoles(solution, rng, minCluesFor(d))
		actual := Rate(givens)
		p := Puzzle{Givens: givens, Solution: solution, Difficulty: actual}
		if actual == d {
			return p
		}
		if !haveBest || diffDistance(actual, d) < diffDistance(best.Difficulty, d) {
			best, haveBest = p, true
		}
	}
	return best
}

// diffDistance returns how many tiers apart a and b are.
func diffDistance(a, b Difficulty) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// generateSolvedGrid produces a complete, valid, randomly-filled grid via
// randomized backtracking.
func generateSolvedGrid(rng *rand.Rand) Grid {
	var g Grid
	fillCell(&g, rng)
	return g
}

// fillCell fills the most-constrained empty cell in g with a randomly
// ordered candidate digit, recursing until g is complete. It returns
// false if no candidate leads to a full solution, in which case it
// undoes its own placement so the caller can backtrack.
func fillCell(g *Grid, rng *rand.Rand) bool {
	row, col, mask, ok := mostConstrainedCell(*g)
	if !ok {
		return true // no empty cells left: g is complete
	}
	digits := candidateDigits(mask)
	rng.Shuffle(len(digits), func(i, j int) { digits[i], digits[j] = digits[j], digits[i] })
	for _, d := range digits {
		g[row][col] = d
		if fillCell(g, rng) {
			return true
		}
	}
	g[row][col] = 0
	return false
}

// minCluesFor returns the minimum number of givens digHoles should leave
// for difficulty d. Lower difficulty tiers keep more clues, matching the
// conventional expectation that an Easy puzzle looks easy (dense with
// givens), not just that it happens to be solvable with basic technique.
// Expert has no floor: dig as far as uniqueness allows, as before.
func minCluesFor(d Difficulty) int {
	switch d {
	case Easy:
		return 40
	case Medium:
		return 32
	case Hard:
		return 28
	default: // Expert
		return 0
	}
}

// digHoles starts from the fully solved grid and clears cells one at a
// time, in random order, keeping each clear only if the resulting givens
// still have a unique solution. It stops once the given count would drop
// below minClues, even if uniqueness would allow removing more.
func digHoles(solution Grid, rng *rand.Rand, minClues int) Grid {
	givens := solution
	clues := 81
	for _, idx := range rng.Perm(81) {
		if clues <= minClues {
			break
		}
		row, col := idx/9, idx%9
		saved := givens[row][col]
		givens[row][col] = 0
		if _, count := Solve(givens); count != 1 {
			givens[row][col] = saved
			continue
		}
		clues--
	}
	return givens
}
