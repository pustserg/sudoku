// Package api contains the REST API handlers.
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
)

// Handler serves the JSON REST API for creating and playing games, plus
// Google OAuth login/logout for API (e.g. future mobile) clients.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
}

// NewHandler returns a Handler. anonStore backs anonymous play; puzzles
// pulls new puzzles; auth and sqlDB back Google login and per-user game
// persistence. auth and sqlDB may be nil in tests that don't exercise
// the authenticated path — storeFor then always falls back to
// anonStore, and the auth endpoints are not expected to be called.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/games", h.createGame)
	mux.HandleFunc("GET /api/games/{id}", h.getGame)
	mux.HandleFunc("POST /api/games/{id}/moves", h.submitMove)
	mux.HandleFunc("POST /api/auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /api/auth/logout", h.logout)
}

// storeFor returns the GameStore to use for r: a user-scoped
// db.GameStore if r carries a valid session, h.anonStore if r carries
// no session, or a non-nil error if Authenticate itself failed (e.g. a
// database error) — that must NOT be treated the same as "no session",
// or a user who thinks they're logged in would silently get an
// unpersisted anonymous game. Callers must check the error and fail the
// request (500) rather than proceeding on the returned store.
//
// Uses auth.FromRequestBearerOnly, not auth.FromRequest: internal/api
// is a bearer-token JSON API and must not also honor the session
// cookie, or it becomes reachable via CSRF from a browser that's logged
// into the web UI (see FromRequestBearerOnly's doc comment).
func (h *Handler) storeFor(r *http.Request) (game.GameStore, error) {
	if h.auth == nil {
		return h.anonStore, nil
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequestBearerOnly(r))
	if err == nil {
		return db.NewGameStore(h.sqlDB, user.ID), nil
	}
	if authFallbackToAnon(err) {
		return h.anonStore, nil
	}
	return nil, err
}

// authFallbackToAnon reports whether an error from auth.Service.
// Authenticate means "no session, proceed anonymously" (true, for
// auth.ErrNoSession) versus a genuine failure that must be propagated
// as a server error rather than silently downgraded (false).
func authFallbackToAnon(err error) bool {
	return errors.Is(err, auth.ErrNoSession)
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

	store, err := h.storeFor(r)
	if err != nil {
		log.Printf("api: authenticate failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not authenticate request")
		return
	}
	g, err := store.Create(r.Context(), p)
	if err != nil {
		log.Printf("api: create game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create game")
		return
	}
	writeJSON(w, http.StatusCreated, toGameState(g))
}

func (h *Handler) getGame(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	store, err := h.storeFor(r)
	if err != nil {
		log.Printf("api: authenticate failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not authenticate request")
		return
	}
	g, err := store.Get(r.Context(), id)
	if errors.Is(err, game.ErrNotFound) {
		writeError(w, http.StatusNotFound, "game not found")
		return
	}
	if err != nil {
		log.Printf("api: get game failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load game")
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

	store, err := h.storeFor(r)
	if err != nil {
		log.Printf("api: authenticate failed: %v", err)
		writeError(w, http.StatusInternalServerError, "could not authenticate request")
		return
	}
	g, err := store.ApplyMove(r.Context(), id, req.Row, req.Col, req.Value)
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

type googleCallbackRequest struct {
	Code string `json:"code"`
}

type googleCallbackResponse struct {
	Token string `json:"token"`
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	var req googleCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, err := h.auth.HandleCallback(r.Context(), req.Code)
	if err != nil {
		log.Printf("api: google callback failed: %v", err)
		writeError(w, http.StatusUnauthorized, "login failed")
		return
	}
	writeJSON(w, http.StatusOK, googleCallbackResponse{Token: token})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), auth.FromRequestBearerOnly(r)); err != nil {
		log.Printf("api: logout failed: %v", err)
		writeError(w, http.StatusInternalServerError, "logout failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
