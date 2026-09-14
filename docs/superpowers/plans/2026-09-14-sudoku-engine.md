# Sudoku Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `internal/sudoku` — grid validation, a uniqueness-aware solver, a technique-based difficulty rater, and a puzzle generator — as pure, dependency-free Go.

**Architecture:** Four components layered on a shared `Grid` type: a validator (used by everything), a backtracking solver with a most-constrained-cell heuristic (used by generation's uniqueness check and reused as the general solve primitive), a technique-simulation difficulty rater, and a generator that fills a full grid then digs holes while preserving uniqueness, retrying to hit a target difficulty.

**Tech Stack:** Go 1.23.1, standard library only (`math/bits`, `math/rand/v2`, `testing`). No external dependencies, per `AGENTS.md`.

**Spec:** `docs/superpowers/specs/2026-09-14-sudoku-engine-design.md`

## Global Constraints

- `internal/sudoku` must have no I/O, no HTTP, no DB dependencies — standard library only (spec "Purpose"; `AGENTS.md`).
- Every change needs table-driven unit tests (`AGENTS.md` "Testing expectations").
- Run `go test ./...` before considering the plan done (`AGENTS.md`).
- Follow `gofmt` formatting.
- Work happens on branch `phase-2-sudoku-engine` (already checked out), not `main`.

---

## File Structure

- `internal/sudoku/grid.go` — `Grid` type, `Valid()`, and shared candidate-bitmask helpers (`candidateMask`, `candidateDigits`, `popcount`) used by the solver, rater, and generator.
- `internal/sudoku/grid_test.go` — tests for `Valid()` and the candidate helpers.
- `internal/sudoku/testdata_test.go` — `mustParseGrid` test helper plus shared known-puzzle string constants, used across test files in the package.
- `internal/sudoku/solver.go` — `Solve()`, the recursive backtracking search, and `mostConstrainedCell()`.
- `internal/sudoku/solver_test.go` — table-driven tests for `Solve()`.
- `internal/sudoku/rater.go` — `Difficulty` type and constants, `Rate()`, the pencil-mark `candidateGrid` type, unit-iteration helpers (`unitCells`, `boxIndex`), `placeDigit`, and the five technique functions (`applyNakedSingle`, `applyHiddenSingle`, `applyLockedCandidates`, `applyNakedPair`, `applyHiddenPair`).
- `internal/sudoku/rater_test.go` — direct tests for each technique function plus end-to-end `Rate()` tests.
- `internal/sudoku/generator.go` — `Puzzle` type, `Generate()`, `generateSolvedGrid`, `fillCell`, `digHoles`, `diffDistance`.
- `internal/sudoku/generator_test.go` — tests for `Generate()`.

`internal/sudoku/doc.go` already exists with the correct package comment — no changes needed.

---

### Task 1: Grid type, validator, and candidate helpers

**Files:**
- Create: `internal/sudoku/grid.go`
- Create: `internal/sudoku/grid_test.go`
- Create: `internal/sudoku/testdata_test.go`

**Interfaces:**
- Produces: `type Grid [9][9]int` (0 = empty, 1-9 = filled); `func (g Grid) Valid() bool`; `func candidateMask(g Grid, row, col int) uint16` (bit `d` set means digit `d` is a legal placement at `(row, col)`; bit 0 is always unset); `func candidateDigits(mask uint16) []int` (ascending digits whose bit is set); `func popcount(mask uint16) int` (count of set bits, via `math/bits`). Produces test helper `func mustParseGrid(t *testing.T, s string) Grid` and constants `wikipediaPuzzle`, `wikipediaSolution`, `oneClueGrid`, `conflictingGivensGrid` (all package-level, used by later test files too).

- [ ] **Step 1: Write the failing test for `Valid()`**

Create `internal/sudoku/testdata_test.go`:

```go
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
```

Create `internal/sudoku/grid_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sudoku/...`
Expected: build failure — `Grid`, `Valid`, `candidateMask`, `candidateDigits`, `popcount` are undefined.

- [ ] **Step 3: Implement `grid.go`**

Create `internal/sudoku/grid.go`:

```go
package sudoku

import "math/bits"

// Grid represents a 9x9 Sudoku board. A zero cell value means empty;
// filled cells hold digits 1-9.
type Grid [9][9]int

// Valid reports whether g contains no duplicate non-zero digit within any
// row, column, or 3x3 box. It does not require g to be complete.
func (g Grid) Valid() bool {
	for row := 0; row < 9; row++ {
		var seen [10]bool
		for col := 0; col < 9; col++ {
			if v := g[row][col]; v != 0 {
				if seen[v] {
					return false
				}
				seen[v] = true
			}
		}
	}
	for col := 0; col < 9; col++ {
		var seen [10]bool
		for row := 0; row < 9; row++ {
			if v := g[row][col]; v != 0 {
				if seen[v] {
					return false
				}
				seen[v] = true
			}
		}
	}
	for boxRow := 0; boxRow < 9; boxRow += 3 {
		for boxCol := 0; boxCol < 9; boxCol += 3 {
			var seen [10]bool
			for r := boxRow; r < boxRow+3; r++ {
				for c := boxCol; c < boxCol+3; c++ {
					if v := g[r][c]; v != 0 {
						if seen[v] {
							return false
						}
						seen[v] = true
					}
				}
			}
		}
	}
	return true
}

// candidateMask returns a bitmask of digits 1-9 (bit d for digit d) that
// could legally be placed at (row, col) given the digits already present
// in that row, column, and 3x3 box. Bit 0 is always unset.
func candidateMask(g Grid, row, col int) uint16 {
	var used uint16
	for i := 0; i < 9; i++ {
		used |= 1 << g[row][i]
		used |= 1 << g[i][col]
	}
	boxRow, boxCol := (row/3)*3, (col/3)*3
	for r := boxRow; r < boxRow+3; r++ {
		for c := boxCol; c < boxCol+3; c++ {
			used |= 1 << g[r][c]
		}
	}
	const digitsMask = uint16(0b1111111110) // bits 1-9
	return digitsMask &^ used
}

// candidateDigits returns the digits (1-9) set in mask, in ascending
// order.
func candidateDigits(mask uint16) []int {
	var out []int
	for d := 1; d <= 9; d++ {
		if mask&(1<<d) != 0 {
			out = append(out, d)
		}
	}
	return out
}

// popcount returns the number of set bits in mask.
func popcount(mask uint16) int {
	return bits.OnesCount16(mask)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sudoku/... -run 'TestGridValid|TestCandidateMask' -v`
Expected: PASS for both `TestGridValid` (all subtests) and `TestCandidateMask`.

- [ ] **Step 5: Commit**

```bash
git add internal/sudoku/grid.go internal/sudoku/grid_test.go internal/sudoku/testdata_test.go
git commit -m "Add Grid type, validator, and candidate-mask helpers"
```

---

### Task 2: Solver

**Files:**
- Create: `internal/sudoku/solver.go`
- Create: `internal/sudoku/solver_test.go`

**Interfaces:**
- Consumes: `Grid` (Task 1), `Grid.Valid()` (Task 1), `candidateMask` / `candidateDigits` (Task 1), test constants and `mustParseGrid` (Task 1).
- Produces: `func Solve(g Grid) (solution Grid, count int)` — `count` is capped at 2. `func mostConstrainedCell(g Grid) (row, col int, mask uint16, ok bool)` (unexported, reused by the generator in Task 4: `ok` is false only when `g` has no empty cells; `mask` is the candidate mask for the returned cell, which is `0` at a dead end).

- [ ] **Step 1: Write the failing test**

Create `internal/sudoku/solver_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sudoku/... -run TestSolve -v`
Expected: build failure — `Solve` is undefined.

- [ ] **Step 3: Implement `solver.go`**

Create `internal/sudoku/solver.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sudoku/... -run TestSolve -v`
Expected: PASS for all four subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/sudoku/solver.go internal/sudoku/solver_test.go
git commit -m "Add backtracking solver with 2-solution cap"
```

---

### Task 3: Difficulty rater

**Files:**
- Create: `internal/sudoku/rater.go`
- Create: `internal/sudoku/rater_test.go`
- Modify: `internal/sudoku/testdata_test.go` (add the `aiEscargotPuzzle` constant)

**Interfaces:**
- Consumes: `Grid`, `candidateMask`, `candidateDigits`, `popcount` (Task 1); test helpers/constants from Task 1.
- Produces: `type Difficulty int` with constants `Easy, Medium, Hard, Expert` (in that increasing order via `iota`); `func Rate(g Grid) Difficulty` (does not mutate `g`). Also produces (unexported, consumed by Task 4's generator only indirectly through `Rate`) `type candidateGrid [9][9]uint16`.

- [ ] **Step 1: Add the AI Escargot fixture and write the failing tests**

Add to `internal/sudoku/testdata_test.go` (append, don't replace existing constants):

```go

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
```

Create `internal/sudoku/rater_test.go`:

```go
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
```

**Note for the implementer:** the `TestRate` expectations for the Wikipedia puzzle (`Easy`) and AI Escargot (`Expert`) are based on published characterizations of these puzzles, not a run of this code. If either assertion fails, first verify the five technique functions are individually correct (the tests above this one check each in isolation) and trace by hand which technique the simulation stalls on, rather than assuming the expected difficulty is wrong and changing it without that check.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sudoku/... -run 'TestRate|TestApply' -v`
Expected: build failure — `Rate`, `candidateGrid`, `applyNakedSingle`, `applyHiddenSingle`, `applyLockedCandidates`, `applyNakedPair`, `applyHiddenPair` are undefined.

- [ ] **Step 3: Implement `rater.go`**

Create `internal/sudoku/rater.go`:

```go
package sudoku

// Difficulty buckets a puzzle by the hardest human solving technique
// needed to complete it, in increasing order.
type Difficulty int

const (
	Easy Difficulty = iota
	Medium
	Hard
	Expert
)

// candidateGrid holds, for each empty cell, a bitmask (bit d for digit d)
// of digits still possible there. Filled cells have mask 0.
type candidateGrid [9][9]uint16

// Rate simulates human solving techniques, in increasing difficulty
// order, to determine how hard g is to solve. It does not mutate g.
func Rate(g Grid) Difficulty {
	values := g
	cands := newCandidateGrid(values)
	hardest := Easy

	for !isComplete(values) {
		switch {
		case applyNakedSingle(&values, &cands):
		case applyHiddenSingle(&values, &cands):
		case applyLockedCandidates(&cands):
			if hardest < Medium {
				hardest = Medium
			}
		case applyNakedPair(&cands):
			if hardest < Hard {
				hardest = Hard
			}
		case applyHiddenPair(&cands):
			if hardest < Hard {
				hardest = Hard
			}
		default:
			// No technique in the set applies and the grid isn't
			// complete: it needs guessing or techniques beyond this
			// list, which makes it genuinely harder than Hard.
			return Expert
		}
	}
	return hardest
}

func isComplete(g Grid) bool {
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			if g[r][c] == 0 {
				return false
			}
		}
	}
	return true
}

func newCandidateGrid(values Grid) candidateGrid {
	var cands candidateGrid
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			if values[r][c] == 0 {
				cands[r][c] = candidateMask(values, r, c)
			}
		}
	}
	return cands
}

// placeDigit fills (row, col) with d in values, clears its own candidate
// mask, and removes d as a candidate from every peer in its row, column,
// and box.
func placeDigit(values *Grid, cands *candidateGrid, row, col, d int) {
	values[row][col] = d
	cands[row][col] = 0
	bit := uint16(1) << d
	for i := 0; i < 9; i++ {
		cands[row][i] &^= bit
		cands[i][col] &^= bit
	}
	boxRow, boxCol := (row/3)*3, (col/3)*3
	for r := boxRow; r < boxRow+3; r++ {
		for c := boxCol; c < boxCol+3; c++ {
			cands[r][c] &^= bit
		}
	}
}

// unitCells returns the 9 (row, col) coordinates belonging to unit:
// 0-8 are rows, 9-17 are columns, 18-26 are 3x3 boxes.
func unitCells(unit int) [9][2]int {
	var cells [9][2]int
	switch {
	case unit < 9:
		r := unit
		for c := 0; c < 9; c++ {
			cells[c] = [2]int{r, c}
		}
	case unit < 18:
		c := unit - 9
		for r := 0; r < 9; r++ {
			cells[r] = [2]int{r, c}
		}
	default:
		b := unit - 18
		br, bc := (b/3)*3, (b%3)*3
		i := 0
		for r := br; r < br+3; r++ {
			for c := bc; c < bc+3; c++ {
				cells[i] = [2]int{r, c}
				i++
			}
		}
	}
	return cells
}

// boxIndex returns which 3x3 box (0-8) (row, col) belongs to.
func boxIndex(row, col int) int {
	return (row/3)*3 + col/3
}

// applyNakedSingle fills the first empty cell that has exactly one
// candidate. Returns whether it made progress.
func applyNakedSingle(values *Grid, cands *candidateGrid) bool {
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			if values[r][c] != 0 {
				continue
			}
			m := cands[r][c]
			if popcount(m) == 1 {
				placeDigit(values, cands, r, c, candidateDigits(m)[0])
				return true
			}
		}
	}
	return false
}

// applyHiddenSingle finds a unit (row, column, or box) where some digit
// is a candidate in exactly one cell, and fills that cell. Returns
// whether it made progress.
func applyHiddenSingle(values *Grid, cands *candidateGrid) bool {
	for unit := 0; unit < 27; unit++ {
		cells := unitCells(unit)
		for d := 1; d <= 9; d++ {
			bit := uint16(1) << d
			count := 0
			var at [2]int
			for _, rc := range cells {
				r, c := rc[0], rc[1]
				if values[r][c] == 0 && cands[r][c]&bit != 0 {
					count++
					at = rc
				}
			}
			if count == 1 {
				placeDigit(values, cands, at[0], at[1], d)
				return true
			}
		}
	}
	return false
}

// applyLockedCandidates eliminates candidates using pointing pairs (a
// digit confined, within a box, to a single row or column) and box-line
// reduction (a digit confined, within a row or column, to a single box).
// Returns whether it eliminated any candidate.
func applyLockedCandidates(cands *candidateGrid) bool {
	for box := 0; box < 9; box++ {
		cells := unitCells(18 + box)
		for d := 1; d <= 9; d++ {
			bit := uint16(1) << d
			rows := map[int]bool{}
			cols := map[int]bool{}
			for _, rc := range cells {
				if cands[rc[0]][rc[1]]&bit != 0 {
					rows[rc[0]] = true
					cols[rc[1]] = true
				}
			}
			if len(rows) == 1 {
				for r := range rows {
					if eliminateFromRowOutsideBox(cands, r, box, bit) {
						return true
					}
				}
			}
			if len(cols) == 1 {
				for c := range cols {
					if eliminateFromColOutsideBox(cands, c, box, bit) {
						return true
					}
				}
			}
		}
	}
	for unit := 0; unit < 18; unit++ {
		cells := unitCells(unit)
		for d := 1; d <= 9; d++ {
			bit := uint16(1) << d
			boxes := map[int]bool{}
			for _, rc := range cells {
				if cands[rc[0]][rc[1]]&bit != 0 {
					boxes[boxIndex(rc[0], rc[1])] = true
				}
			}
			if len(boxes) == 1 {
				for b := range boxes {
					if eliminateFromBoxOutsideUnit(cands, b, unit, bit) {
						return true
					}
				}
			}
		}
	}
	return false
}

func eliminateFromRowOutsideBox(cands *candidateGrid, row, box int, bit uint16) bool {
	boxCol := (box % 3) * 3
	changed := false
	for c := 0; c < 9; c++ {
		if c >= boxCol && c < boxCol+3 {
			continue
		}
		if cands[row][c]&bit != 0 {
			cands[row][c] &^= bit
			changed = true
		}
	}
	return changed
}

func eliminateFromColOutsideBox(cands *candidateGrid, col, box int, bit uint16) bool {
	boxRow := (box / 3) * 3
	changed := false
	for r := 0; r < 9; r++ {
		if r >= boxRow && r < boxRow+3 {
			continue
		}
		if cands[r][col]&bit != 0 {
			cands[r][col] &^= bit
			changed = true
		}
	}
	return changed
}

func eliminateFromBoxOutsideUnit(cands *candidateGrid, box, unit int, bit uint16) bool {
	br, bc := (box/3)*3, (box%3)*3
	changed := false
	for r := br; r < br+3; r++ {
		for c := bc; c < bc+3; c++ {
			inUnit := (unit < 9 && r == unit) || (unit >= 9 && c == unit-9)
			if inUnit {
				continue
			}
			if cands[r][c]&bit != 0 {
				cands[r][c] &^= bit
				changed = true
			}
		}
	}
	return changed
}

// applyNakedPair finds two cells in a unit that share an identical
// 2-digit candidate set, and eliminates those two digits from every
// other cell in the unit. Returns whether it eliminated any candidate.
func applyNakedPair(cands *candidateGrid) bool {
	for unit := 0; unit < 27; unit++ {
		cells := unitCells(unit)
		for i := 0; i < 9; i++ {
			ri, ci := cells[i][0], cells[i][1]
			mi := cands[ri][ci]
			if popcount(mi) != 2 {
				continue
			}
			for j := i + 1; j < 9; j++ {
				rj, cj := cells[j][0], cells[j][1]
				if cands[rj][cj] != mi {
					continue
				}
				changed := false
				for k := 0; k < 9; k++ {
					if k == i || k == j {
						continue
					}
					rk, ck := cells[k][0], cells[k][1]
					if cands[rk][ck]&mi != 0 {
						cands[rk][ck] &^= mi
						changed = true
					}
				}
				if changed {
					return true
				}
			}
		}
	}
	return false
}

// applyHiddenPair finds two digits confined, within a unit, to the same
// two cells, and restricts those cells' candidates to exactly those two
// digits. Returns whether it changed any cell.
func applyHiddenPair(cands *candidateGrid) bool {
	for unit := 0; unit < 27; unit++ {
		cells := unitCells(unit)
		var digitCells [10]uint16 // bit i set means cells[i] has this digit as a candidate
		for i, rc := range cells {
			m := cands[rc[0]][rc[1]]
			for d := 1; d <= 9; d++ {
				if m&(1<<d) != 0 {
					digitCells[d] |= 1 << i
				}
			}
		}
		for d1 := 1; d1 <= 9; d1++ {
			if popcount(digitCells[d1]) != 2 {
				continue
			}
			for d2 := d1 + 1; d2 <= 9; d2++ {
				if digitCells[d2] != digitCells[d1] {
					continue
				}
				pairMask := uint16(1)<<d1 | uint16(1)<<d2
				changed := false
				for i := 0; i < 9; i++ {
					if digitCells[d1]&(1<<i) == 0 {
						continue
					}
					r, c := cells[i][0], cells[i][1]
					if cands[r][c] != pairMask {
						cands[r][c] = pairMask
						changed = true
					}
				}
				if changed {
					return true
				}
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sudoku/... -run 'TestRate|TestApply' -v`
Expected: PASS for `TestRate` and all five `TestApply*` tests. If `TestRate` fails on the Wikipedia or AI Escargot cases, follow the note above Step 2 before changing any expected value.

- [ ] **Step 5: Commit**

```bash
git add internal/sudoku/rater.go internal/sudoku/rater_test.go internal/sudoku/testdata_test.go
git commit -m "Add technique-based difficulty rater"
```

---

### Task 4: Generator

**Files:**
- Create: `internal/sudoku/generator.go`
- Create: `internal/sudoku/generator_test.go`

**Interfaces:**
- Consumes: `Grid`, `Grid.Valid()`, `candidateDigits` (Task 1); `Solve`, `mostConstrainedCell` (Task 2); `Difficulty` and its constants, `Rate` (Task 3).
- Produces: `type Puzzle struct { Givens, Solution Grid; Difficulty Difficulty }`; `func Generate(d Difficulty, rng *rand.Rand) Puzzle` (`rng` from `math/rand/v2`).

- [ ] **Step 1: Write the failing tests**

Create `internal/sudoku/generator_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sudoku/... -run TestGenerate -v`
Expected: build failure — `Generate`, `Puzzle`, `diffDistance` are undefined.

- [ ] **Step 3: Implement `generator.go`**

Create `internal/sudoku/generator.go`:

```go
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
		givens := digHoles(solution, rng)
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

// digHoles starts from the fully solved grid and clears cells one at a
// time, in random order, keeping each clear only if the resulting givens
// still have a unique solution.
func digHoles(solution Grid, rng *rand.Rand) Grid {
	givens := solution
	for _, idx := range rng.Perm(81) {
		row, col := idx/9, idx%9
		saved := givens[row][col]
		givens[row][col] = 0
		if _, count := Solve(givens); count != 1 {
			givens[row][col] = saved
		}
	}
	return givens
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sudoku/... -run TestGenerate -v`
Expected: PASS for both tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sudoku/generator.go internal/sudoku/generator_test.go
git commit -m "Add puzzle generator with difficulty targeting"
```

---

### Task 5: Full package verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full test suite**

Run: `go test ./... -v`
Expected: PASS for every test in `internal/sudoku` (and no regressions elsewhere).

- [ ] **Step 2: Check formatting**

Run: `gofmt -l internal/sudoku`
Expected: no output (no files need formatting). If any file is listed, run `gofmt -w` on it and re-run Step 1.

- [ ] **Step 3: Run go vet**

Run: `go vet ./internal/sudoku/...`
Expected: no output.

- [ ] **Step 4: Commit any formatting fixes (only if Step 2 found something)**

```bash
git add internal/sudoku
git commit -m "gofmt internal/sudoku"
```
