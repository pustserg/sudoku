// Package api contains the REST API handlers.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// Handler serves the JSON REST API for creating and playing games.
type Handler struct {
	store   *game.Store
	puzzles game.PuzzleLookup
}

// NewHandler returns a Handler backed by store, pulling new puzzles via
// puzzles.
func NewHandler(store *game.Store, puzzles game.PuzzleLookup) *Handler {
	return &Handler{store: store, puzzles: puzzles}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/games", h.createGame)
	mux.HandleFunc("GET /api/games/{id}", h.getGame)
	mux.HandleFunc("POST /api/games/{id}/moves", h.submitMove)
}

type gameState struct {
	ID          string      `json:"id"`
	Givens      sudoku.Grid `json:"givens"`
	Current     sudoku.Grid `json:"current"`
	Difficulty  string      `json:"difficulty"`
	Solved      bool        `json:"solved"`
	Mistakes    int         `json:"mistakes"`
	MaxMistakes int         `json:"maxMistakes"`
	Failed      bool        `json:"failed"`
}

func toGameState(g *game.Game) gameState {
	return gameState{
		ID:          g.ID,
		Givens:      g.Givens,
		Current:     g.Current,
		Difficulty:  game.DifficultyName(g.Difficulty),
		Solved:      g.Solved(),
		Mistakes:    g.Mistakes,
		MaxMistakes: g.MaxMistakes,
		Failed:      g.Failed(),
	}
}

type createGameRequest struct {
	Difficulty string `json:"difficulty"`
}

func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	d, ok := game.ParseDifficulty(req.Difficulty)
	if !ok {
		writeError(w, http.StatusBadRequest, "difficulty must be one of easy, medium, hard, expert")
		return
	}

	p, err := h.puzzles(r.Context(), d)
	if err != nil {
		log.Printf("api: puzzle lookup failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not find a puzzle right now")
		return
	}

	g := h.store.Create(p)
	writeJSON(w, http.StatusCreated, toGameState(g))
}

func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, ok := h.store.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	writeJSON(w, http.StatusOK, toGameState(g))
}

type submitMoveRequest struct {
	Row   int `json:"row"`
	Col   int `json:"col"`
	Value int `json:"value"`
}

func (h *Handler) submitMove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req submitMoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	g, err := h.store.ApplyMove(id, req.Row, req.Col, req.Value)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, toGameState(g))
	case errors.Is(err, game.ErrNotFound):
		writeError(w, http.StatusNotFound, "game not found")
	case errors.Is(err, game.ErrOutOfRange), errors.Is(err, game.ErrGivenCell):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, game.ErrGameOver):
		writeError(w, http.StatusConflict, err.Error())
	default:
		log.Printf("api: unexpected ApplyMove error: %v", err)
		writeError(w, http.StatusInternalServerError, "could not apply move")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
