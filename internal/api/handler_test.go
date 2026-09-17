package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

func testHandler() (*Handler, *game.Store) {
	store := game.NewStore()
	lookup := func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		var givens, solution sudoku.Grid
		givens[0][0] = 5
		solution[0][0] = 5
		solution[0][1] = 3
		return sudoku.Puzzle{Givens: givens, Solution: solution, Difficulty: d}, nil
	}
	return NewHandler(store, lookup), store
}

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

func TestCreateGame(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"easy", `{"difficulty":"easy"}`, http.StatusCreated},
		{"medium", `{"difficulty":"medium"}`, http.StatusCreated},
		{"hard", `{"difficulty":"hard"}`, http.StatusCreated},
		{"expert", `{"difficulty":"expert"}`, http.StatusCreated},
		{"invalid difficulty", `{"difficulty":"nightmare"}`, http.StatusBadRequest},
		{"missing difficulty", `{}`, http.StatusBadRequest},
		{"invalid body", `not json`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := testHandler()
			mux := newMux(h)

			req := httptest.NewRequest(http.MethodPost, "/api/games", bytes.NewBufferString(tt.body))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus == http.StatusCreated {
				var state gameState
				if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
					t.Fatalf("unmarshal response: %v", err)
				}
				if state.ID == "" {
					t.Error("response has empty id")
				}
				if state.Solved {
					t.Error("newly created game reports solved = true")
				}
			}
		})
	}
}

func TestGetGame(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/games/"+g.ID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.ID != g.ID {
		t.Errorf("id = %q, want %q", state.ID, g.ID)
	}
}

func TestGetGameNotFound(t *testing.T) {
	h, _ := testHandler()
	mux := newMux(h)

	req := httptest.NewRequest(http.MethodGet, "/api/games/does-not-exist", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSubmitMove(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	givens[0][0] = 5
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":0,"col":1,"value":7}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.Current[0][1] != 7 {
		t.Errorf("current[0][1] = %d, want 7", state.Current[0][1])
	}
}

func TestSubmitMoveOnGivenCell(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	givens[0][0] = 5
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":0,"col":0,"value":9}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSubmitMoveOutOfRange(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy})
	mux := newMux(h)

	body := `{"row":9,"col":0,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestSubmitMoveUnknownGame(t *testing.T) {
	h, _ := testHandler()
	mux := newMux(h)

	body := `{"row":0,"col":0,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/does-not-exist/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestSubmitMoveWrongValueIncrementsMistakes(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	givens[0][0] = 5
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Easy}) // solution[0][1] == 0

	body := `{"row":0,"col":1,"value":9}` // wrong: solution wants 0 here
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux := newMux(h)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var state gameState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if state.Mistakes != 1 {
		t.Errorf("mistakes = %d, want 1", state.Mistakes)
	}
	if state.MaxMistakes != 5 {
		t.Errorf("maxMistakes = %d, want 5 for Easy", state.MaxMistakes)
	}
	if state.Failed {
		t.Errorf("failed = true after 1 of 5 mistakes, want false")
	}
}

func TestSubmitMoveAfterGameOver(t *testing.T) {
	h, store := testHandler()
	var givens sudoku.Grid
	g := store.Create(sudoku.Puzzle{Givens: givens, Solution: givens, Difficulty: sudoku.Hard}) // MaxMistakes == 3, solution[0][1] == 0
	mux := newMux(h)

	for i := 0; i < 3; i++ {
		body := `{"row":0,"col":1,"value":9}` // always wrong
		req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("mistake %d: status = %d, want %d (body: %s)", i+1, rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	body := `{"row":0,"col":2,"value":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/games/"+g.ID+"/moves", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status after game over = %d, want %d (body: %s)", rec.Code, http.StatusConflict, rec.Body.String())
	}
}
