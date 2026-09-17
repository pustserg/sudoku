package main

import "github.com/pustserg/sudoku/internal/sudoku"

// buildJobs returns a slice with one Difficulty value per puzzle to
// generate: easy puzzles first, then medium, then hard, then expert.
func buildJobs(easy, medium, hard, expert int) []sudoku.Difficulty {
	jobs := make([]sudoku.Difficulty, 0, easy+medium+hard+expert)
	for i := 0; i < easy; i++ {
		jobs = append(jobs, sudoku.Easy)
	}
	for i := 0; i < medium; i++ {
		jobs = append(jobs, sudoku.Medium)
	}
	for i := 0; i < hard; i++ {
		jobs = append(jobs, sudoku.Hard)
	}
	for i := 0; i < expert; i++ {
		jobs = append(jobs, sudoku.Expert)
	}
	return jobs
}
