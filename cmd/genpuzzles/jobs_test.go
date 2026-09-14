package main

import (
	"testing"

	"github.com/pustserg/sudoku/internal/sudoku"
)

func TestBuildJobs(t *testing.T) {
	jobs := buildJobs(2, 1, 0, 3)

	if len(jobs) != 6 {
		t.Fatalf("len(jobs) = %d, want 6", len(jobs))
	}

	counts := map[sudoku.Difficulty]int{}
	for _, d := range jobs {
		counts[d]++
	}
	want := map[sudoku.Difficulty]int{
		sudoku.Easy:   2,
		sudoku.Medium: 1,
		sudoku.Expert: 3,
	}
	for d, wantCount := range want {
		if counts[d] != wantCount {
			t.Errorf("counts[%v] = %d, want %d", d, counts[d], wantCount)
		}
	}
	if counts[sudoku.Hard] != 0 {
		t.Errorf("counts[Hard] = %d, want 0", counts[sudoku.Hard])
	}

	if jobs[0] != sudoku.Easy || jobs[1] != sudoku.Easy {
		t.Errorf("expected first two jobs to be Easy, got %v", jobs[:2])
	}
	if jobs[2] != sudoku.Medium {
		t.Errorf("expected third job to be Medium, got %v", jobs[2])
	}
	if jobs[3] != sudoku.Expert || jobs[4] != sudoku.Expert || jobs[5] != sudoku.Expert {
		t.Errorf("expected last three jobs to be Expert, got %v", jobs[3:])
	}
}

func TestBuildJobsAllZero(t *testing.T) {
	jobs := buildJobs(0, 0, 0, 0)
	if len(jobs) != 0 {
		t.Errorf("len(jobs) = %d, want 0", len(jobs))
	}
}
