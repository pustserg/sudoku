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

func TestGenerateRespectsMinClueFloor(t *testing.T) {
	// Regression test: digHoles used to dig every puzzle down to the
	// bare uniqueness minimum (~23-27 clues) regardless of difficulty,
	// so an "Easy" puzzle looked just as sparse as an "Expert" one. Each
	// tier must retain at least its configured floor of givens.
	for _, d := range []Difficulty{Easy, Medium, Hard, Expert} {
		for seed := uint64(0); seed < 5; seed++ {
			rng := rand.New(rand.NewPCG(uint64(d)*10+seed, seed+1))
			p := Generate(d, rng)

			clues := 0
			for r := 0; r < 9; r++ {
				for c := 0; c < 9; c++ {
					if p.Givens[r][c] != 0 {
						clues++
					}
				}
			}

			want := minCluesFor(d)
			if clues < want {
				t.Errorf("Generate(%v) seed %d: %d clues, want at least %d", d, seed, clues, want)
			}
		}
	}
}

func TestGenerateTargetsRequestedDifficulty(t *testing.T) {
	// The underlying difficulty distribution from digging holes is bimodal
	// (Medium and Hard are comparatively rare before Generate's internal
	// retry compensates), so a single seed per tier could land 2+ tiers
	// away just from bad luck. Use several seeds per tier to give real
	// margin against an unlucky seed while still catching an actual
	// regression in difficulty targeting.
	const seedsPerTier = 5

	for _, d := range []Difficulty{Easy, Medium, Hard, Expert} {
		for i := uint64(0); i < seedsPerTier; i++ {
			seed := uint64(d)*seedsPerTier + i
			rng := rand.New(rand.NewPCG(seed, seed+1))
			p := Generate(d, rng)

			// Generate retries internally (see maxGenerateAttempts) but isn't
			// guaranteed to hit the exact requested tier every time, so allow
			// landing one tier away.
			if got := diffDistance(p.Difficulty, d); got > 1 {
				t.Errorf("Generate(%v) seed %d: actual difficulty %v is %d tiers away, want within 1", d, seed, p.Difficulty, got)
			}

			// This part of the contract is deterministic, not statistical:
			// Generate must always report the difficulty its own puzzle
			// actually rates as, regardless of whether that matches the
			// requested tier.
			if rated := Rate(p.Givens); p.Difficulty != rated {
				t.Errorf("Generate(%v) seed %d: p.Difficulty = %v, but Rate(p.Givens) = %v", d, seed, p.Difficulty, rated)
			}
		}
	}
}
