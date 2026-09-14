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
