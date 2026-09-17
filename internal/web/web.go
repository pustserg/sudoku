// Package web contains the htmx page handlers and templates.
package web

import (
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/pustserg/sudoku/internal/game"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// Handler serves the htmx web UI.
type Handler struct {
	store   game.GameStore
	puzzles game.PuzzleLookup
}

// NewHandler returns a Handler backed by store, pulling new puzzles via
// puzzles.
func NewHandler(store game.GameStore, puzzles game.PuzzleLookup) *Handler {
	return &Handler{store: store, puzzles: puzzles}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("POST /play", h.createGame)
	mux.HandleFunc("GET /play/{id}", h.showBoard)
	mux.HandleFunc("POST /play/{id}/moves", h.submitMove)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	if err := templates.ExecuteTemplate(w, "home", nil); err != nil {
		log.Printf("web: render home: %v", err)
	}
}

func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	d, ok := game.ParseDifficulty(r.FormValue("difficulty"))
	if !ok {
		http.Error(w, "invalid difficulty", http.StatusBadRequest)
		return
	}

	p, err := h.puzzles(r.Context(), d)
	if err != nil {
		log.Printf("web: puzzle lookup failed: %v", err)
		http.Error(w, "could not find a puzzle right now", http.StatusInternalServerError)
		return
	}

	g, err := h.store.Create(r.Context(), p)
	if err != nil {
		log.Printf("web: create game failed: %v", err)
		http.Error(w, "could not create game", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/play/"+g.ID, http.StatusFound)
}

// cellView is the template data for one board cell.
type cellView struct {
	Row, Col int
	Value    int
	Given    bool
	// Wrong is true when this cell holds a player-entered value that
	// doesn't match the puzzle's solution. The solution digit itself is
	// never exposed to the template/client — only this boolean.
	Wrong bool
}

// boardView is the template data for both the full board page and the
// htmx board fragment.
type boardView struct {
	ID             string
	DifficultyName string
	Solved         bool
	Failed         bool
	Mistakes       int
	MaxMistakes    int
	Cells          [9][9]cellView
}

func newBoardView(g *game.Game) boardView {
	var cells [9][9]cellView
	for r := 0; r < 9; r++ {
		for c := 0; c < 9; c++ {
			given := g.Givens[r][c] != 0
			value := g.Current[r][c]
			cells[r][c] = cellView{
				Row:   r,
				Col:   c,
				Value: value,
				Given: given,
				Wrong: !given && value != 0 && value != g.Solution[r][c],
			}
		}
	}
	return boardView{
		ID:             g.ID,
		DifficultyName: game.DifficultyName(g.Difficulty),
		Solved:         g.Solved(),
		Failed:         g.Failed(),
		Mistakes:       g.Mistakes,
		MaxMistakes:    g.MaxMistakes,
		Cells:          cells,
	}
}

func (h *Handler) showBoard(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	g, err := h.store.Get(r.Context(), id)
	if errors.Is(err, game.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Printf("web: get game failed: %v", err)
		http.Error(w, "could not load game", http.StatusInternalServerError)
		return
	}
	if err := templates.ExecuteTemplate(w, "boardPage", newBoardView(g)); err != nil {
		log.Printf("web: render board page: %v", err)
	}
}

func (h *Handler) submitMove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	row, rowErr := strconv.Atoi(r.FormValue("row"))
	col, colErr := strconv.Atoi(r.FormValue("col"))
	value, valErr := strconv.Atoi(r.FormValue("value"))
	if rowErr != nil || colErr != nil || valErr != nil {
		http.Error(w, "invalid move", http.StatusBadRequest)
		return
	}

	g, err := h.store.ApplyMove(r.Context(), id, row, col, value)
	switch {
	case err == nil:
		if err := templates.ExecuteTemplate(w, "board", newBoardView(g)); err != nil {
			log.Printf("web: render board fragment: %v", err)
		}
	case errors.Is(err, game.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, game.ErrOutOfRange), errors.Is(err, game.ErrGivenCell):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, game.ErrGameOver):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		log.Printf("web: unexpected ApplyMove error: %v", err)
		http.Error(w, "could not apply move", http.StatusInternalServerError)
	}
}
