package sudoku

import (
	"math/rand/v2"
	"testing"
)

func TestGenerateProducesValidUniquePuzzle(t *testing.T) {
	for seed := uint64(0); seed < 10; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed+1))
		p := Generate(Hard, rng)

		if !p.Givens.Valid() {
			t.Fatalf("seed %d: givens are invalid: %v", seed, p.Givens)
		}
		if !p.Solution.Valid() {
			t.Fatalf("seed %d: solution is invalid: %v", seed, p.Solution)
		}
		solved, count := Solve(p.Givens)
		if count != 1 {
			t.Fatalf("seed %d: givens have %d solutions, want exactly 1", seed, count)
		}
		if solved != p.Solution {
			t.Fatalf("seed %d: solver's solution does not match the puzzle's recorded solution", seed)
		}
	}
}

func TestGenerateTargetsRequestedDifficulty(t *testing.T) {
	for _, d := range []Difficulty{Easy, Medium, Hard, Expert} {
		rng := rand.New(rand.NewPCG(uint64(d), uint64(d)+1))
		p := Generate(d, rng)

		// Generate retries internally (see maxGenerateAttempts) but isn't
		// guaranteed to hit the exact requested tier every time, so allow
		// landing one tier away.
		if got := diffDistance(p.Difficulty, d); got > 1 {
			t.Errorf("Generate(%v): actual difficulty %v is %d tiers away, want within 1", d, p.Difficulty, got)
		}
	}
}
