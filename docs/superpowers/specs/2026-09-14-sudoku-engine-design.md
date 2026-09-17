# Sudoku engine design (`internal/sudoku`)

Date: 2026-09-14
Status: Approved for implementation
Part of: Roadmap Phase 2, sub-project 1 of 3 (engine → puzzle pool → play loop)

## Purpose

Provide the pure Go, dependency-free game logic that both the offline
`cmd/genpuzzles` CLI and the runtime server rely on: representing a grid,
validating it, solving it, generating new puzzles, and rating their
difficulty. Per `AGENTS.md`, this package must have no I/O, no HTTP, no DB
dependencies, and is the single source of truth for game-logic decisions.

## Types

```go
type Grid [9][9]int // 0 = empty cell, 1-9 = filled

type Difficulty int

const (
    Easy Difficulty = iota
    Medium
    Hard
    Expert
)

type Puzzle struct {
    Givens     Grid
    Solution   Grid
    Difficulty Difficulty
}
```

`Grid` is a plain fixed-size array — cheap to copy, simple to reason about,
no pointer aliasing surprises during backtracking.

## Components

### 1. Validator

`func (g Grid) Valid() bool` — true if no row, column, or 3x3 box contains a
duplicate non-zero digit. Does not require the grid to be complete. Used by
every other component as a building block.

### 2. Solver

`func Solve(g Grid) (solution Grid, count int)`

Backtracking solver using constraint propagation: at each step, pick the
empty cell with the fewest remaining candidate digits (most-constrained-cell
heuristic) to keep branching factor low. Counts distinct solutions but
**stops as soon as it finds 2**, since callers only ever need "0", "1", or
"2+" — this keeps uniqueness checks fast during generation.

This is the single reusable primitive: generation's uniqueness check and
any future "solve this puzzle" API feature both call it.

### 3. Generator

`func Generate(d Difficulty, rng *rand.Rand) Puzzle`

1. Produce a fully solved grid via randomized backtracking (shuffle
   candidate order at each cell) starting from an empty grid.
2. Dig holes: visit cells in random order, tentatively clear each one, and
   run the solver's uniqueness check on the resulting givens. If the puzzle
   is still uniquely solvable, keep the cell cleared; otherwise put the
   digit back and move on.
3. After digging as many holes as uniqueness allows, rate the resulting
   puzzle with the rater (below). If the rated difficulty doesn't match the
   requested `d`, the generator retries with a fresh full grid (bounded
   number of attempts) and, if it still can't hit the target after that
   budget, returns the closest puzzle it produced along with its *actual*
   rated difficulty rather than looping indefinitely. Callers (the CLI) are
   expected to bucket puzzles by their actual rated difficulty rather than
   assume the requested one was hit exactly.

`rng` is passed in explicitly (no global/package-level randomness) so tests
and the CLI can seed deterministically.

### 4. Difficulty rater

`func Rate(g Grid) Difficulty`

Simulates human solving using an ordered set of techniques, applying the
cheapest applicable technique repeatedly until the grid is solved or no
technique applies:

1. Naked single
2. Hidden single
3. Locked candidates (pointing pairs / box-line reduction)
4. Naked pair
5. Hidden pair

The rater tracks the hardest technique actually needed. Mapping to
`Difficulty`:

- Only naked/hidden singles needed → **Easy**
- Locked candidates needed at least once → **Medium**
- Naked/hidden pairs needed → **Hard**
- Simulation stalls (no listed technique applies, puzzle isn't fully
  solved) → **Expert** (implies the puzzle needs guessing or techniques
  beyond this list — genuinely harder, so Expert is the correct bucket
  rather than an error)

This mapping is intentionally simple for v1; if playtesting shows the
buckets are miscalibrated, thresholds can be tuned without changing the
public API.

## Testing

Table-driven tests per component, in `internal/sudoku`:

- **Validator**: known-valid and known-invalid grids (row/col/box
  duplicates, partially-filled grids).
- **Solver**: fixed puzzles with a known unique solution, a fixed puzzle
  with multiple solutions (confirms `count == 2` early-exit), an already-
  solved grid, an unsolvable grid.
- **Rater**: hand-picked puzzles of each known difficulty tier (sourced
  from published easy/medium/hard/expert puzzles) asserting the expected
  bucket.
- **Generator**: smoke tests across many seeds asserting each generated
  puzzle is valid, has a unique solution, and completes within a
  reasonable time bound (no infinite loops); one test per requested
  difficulty confirming the generator can hit or reasonably approximate it.

## Out of scope

- Hint generation / step-by-step solve explanations.
- Advanced techniques (X-wing, swordfish, etc.) — puzzles requiring them
  simply land in Expert.
- Persistence, HTTP, CLI wiring — those belong to sub-projects 2 and 3.
