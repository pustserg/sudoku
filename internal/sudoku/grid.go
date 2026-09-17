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
