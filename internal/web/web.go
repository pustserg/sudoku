// Package web contains the htmx page handlers and templates.
package web

import (
	"database/sql"
	"embed"
	"errors"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
)

//go:embed templates/*.html
var templateFS embed.FS

var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// stateCookieName holds the anti-CSRF state value between the redirect
// to Google and the callback. Short-lived and httpOnly; never read by
// anything but googleCallback.
const stateCookieName = "oauth_state"

// sessionCookieMaxAge matches auth.sessionLifetime (30 days), so the
// browser doesn't keep sending a cookie the server has already expired.
const sessionCookieMaxAge = 30 * 24 * 60 * 60

// Handler serves the htmx web UI, including Google OAuth login/logout.
type Handler struct {
	anonStore game.GameStore
	puzzles   game.PuzzleLookup
	auth      *auth.Service
	sqlDB     *sql.DB
}

// NewHandler returns a Handler. See api.NewHandler's doc comment for
// the meaning of each parameter — the two packages mirror each other.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("POST /play", h.createGame)
	mux.HandleFunc("GET /play/{id}", h.showBoard)
	mux.HandleFunc("POST /play/{id}/moves", h.submitMove)
	mux.HandleFunc("GET /auth/google/login", h.googleLogin)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /logout", h.logout)
}

// storeFor mirrors api.Handler.storeFor: a user-scoped db.GameStore if r
// carries a valid session, otherwise h.anonStore.
func (h *Handler) storeFor(r *http.Request) game.GameStore {
	if h.auth == nil {
		return h.anonStore
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		return h.anonStore
	}
	return db.NewGameStore(h.sqlDB, user.ID)
}

// currentUserEmail returns the signed-in user's email for r, or "" if
// anonymous — used only to decide what the home page's header shows.
func (h *Handler) currentUserEmail(r *http.Request) string {
	if h.auth == nil {
		return ""
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		return ""
	}
	return user.Email
}

type homeView struct {
	LoggedIn bool
	Email    string
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	email := h.currentUserEmail(r)
	view := homeView{LoggedIn: email != "", Email: email}
	if err := templates.ExecuteTemplate(w, "home", view); err != nil {
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

	g, err := h.storeFor(r).Create(r.Context(), p)
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
	g, err := h.storeFor(r).Get(r.Context(), id)
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

	g, err := h.storeFor(r).ApplyMove(r.Context(), id, row, col, value)
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

func (h *Handler) googleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := auth.RandomToken(16)
	if err != nil {
		log.Printf("web: generate oauth state: %v", err)
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   600, // 10 minutes: long enough for a login round trip, short-lived by design
	})
	http.Redirect(w, r, h.auth.LoginURL(state), http.StatusFound)
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: "/", MaxAge: -1})

	token, err := h.auth.HandleCallback(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("web: oauth callback: %v", err)
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		MaxAge:   sessionCookieMaxAge,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), auth.FromRequest(r)); err != nil {
		log.Printf("web: logout: %v", err)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusFound)
}
