// Package web contains the htmx page handlers and templates.
package web

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"

	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
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
	// secureCookies sets the Secure attribute on the session and OAuth
	// state cookies. It must be false for local HTTP development
	// (http://localhost:...) — a Secure cookie is never sent back by the
	// browser over plain HTTP, which would break login entirely — and
	// true in any real deployment, which is always HTTPS. Callers derive
	// this from whether the configured Google OAuth redirect URL is
	// https://, rather than hardcoding it, so dev and prod both work
	// without a separate flag.
	secureCookies bool
}

// NewHandler returns a Handler. See api.NewHandler's doc comment for
// the meaning of anonStore/puzzles/authSvc/sqlDB — the two packages
// mirror each other for those. secureCookies controls the Secure
// attribute on cookies this Handler sets; see the Handler.secureCookies
// field doc for how callers should derive it.
func NewHandler(anonStore game.GameStore, puzzles game.PuzzleLookup, authSvc *auth.Service, sqlDB *sql.DB, secureCookies bool) *Handler {
	return &Handler{anonStore: anonStore, puzzles: puzzles, auth: authSvc, sqlDB: sqlDB, secureCookies: secureCookies}
}

// Register adds this Handler's routes to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /stats", h.stats)
	mux.HandleFunc("POST /play", h.createGame)
	mux.HandleFunc("GET /play/{id}", h.showBoard)
	mux.HandleFunc("POST /play/{id}/moves", h.submitMove)
	mux.HandleFunc("GET /auth/google/login", h.googleLogin)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
	mux.HandleFunc("POST /logout", h.logout)
}

// storeFor mirrors api.Handler.storeFor: returns the GameStore to use
// for r plus the id of the authenticated user it belongs to (0 for
// anonymous — safe, since users.id is a BIGSERIAL starting at 1). A
// user-scoped db.GameStore if r carries a valid session, h.anonStore if
// r carries no session, or a non-nil error if Authenticate itself
// failed (e.g. a database error) — that must NOT be treated the same as
// "no session", or a user who thinks they're logged in would silently
// get an unpersisted anonymous game. Callers must check the error and
// fail the request (500) rather than proceeding on the returned store.
//
// Unlike internal/api, this legitimately uses auth.FromRequest (cookie
// then bearer), since the web UI's clients are cookie-authenticated
// browsers.
func (h *Handler) storeFor(r *http.Request) (game.GameStore, int64, error) {
	if h.auth == nil {
		return h.anonStore, 0, nil
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err == nil {
		return db.NewGameStore(h.sqlDB, user.ID), user.ID, nil
	}
	if authFallbackToAnon(err) {
		return h.anonStore, 0, nil
	}
	return nil, 0, err
}

// authFallbackToAnon mirrors api.authFallbackToAnon: reports whether an
// error from auth.Service.Authenticate means "no session, proceed
// anonymously" (true, for auth.ErrNoSession) versus a genuine failure
// that must be propagated as a server error (false).
func authFallbackToAnon(err error) bool {
	return errors.Is(err, auth.ErrNoSession)
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

// difficultyStatsView is the template data for one difficulty's row in
// the stats table.
type difficultyStatsView struct {
	Name         string
	Played       int
	Solved       int
	Failed       int
	WinRatePct   int    // 0-100; 0 when Played == 0
	AvgMistakes  string // formatted to 1 decimal, "—" when Played == 0
	BestMistakes string // "—" when Solved == 0
}

// recentGameView is the template data for one row in the recent-games
// history list.
type recentGameView struct {
	DifficultyName string
	Solved         bool
	Mistakes       int
	When           string
}

// statsView is the template data for the /stats page.
type statsView struct {
	Email       string
	Difficulty  []difficultyStatsView
	RecentGames []recentGameView
}

func newStatsView(email string, stats []db.DifficultyStats, recent []db.GameSummary) statsView {
	view := statsView{Email: email, Difficulty: make([]difficultyStatsView, len(stats))}
	for i, s := range stats {
		winRate := 0
		if s.Played > 0 {
			winRate = int(float64(s.Solved) / float64(s.Played) * 100)
		}
		avg := "—"
		if s.Played > 0 {
			avg = fmt.Sprintf("%.1f", s.AvgMistakes)
		}
		best := "—"
		if s.BestMistakes != nil {
			best = fmt.Sprintf("%d", *s.BestMistakes)
		}
		view.Difficulty[i] = difficultyStatsView{
			Name:         game.DifficultyName(s.Difficulty),
			Played:       s.Played,
			Solved:       s.Solved,
			Failed:       s.Failed,
			WinRatePct:   winRate,
			AvgMistakes:  avg,
			BestMistakes: best,
		}
	}
	view.RecentGames = make([]recentGameView, len(recent))
	for i, g := range recent {
		view.RecentGames[i] = recentGameView{
			DifficultyName: game.DifficultyName(g.Difficulty),
			Solved:         g.Status == "solved",
			Mistakes:       g.Mistakes,
			When:           g.UpdatedAt.Format("02 Jan 2006 15:04"),
		}
	}
	return view
}

// recentGamesLimit is how many of a user's most recent finished games
// the /stats page's history list shows.
const recentGamesLimit = 20

func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	user, err := h.auth.Authenticate(r.Context(), auth.FromRequest(r))
	if err != nil {
		if authFallbackToAnon(err) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		log.Printf("web: authenticate failed: %v", err)
		http.Error(w, "could not authenticate request", http.StatusInternalServerError)
		return
	}

	stats, err := db.UserStats(r.Context(), h.sqlDB, user.ID)
	if err != nil {
		log.Printf("web: user stats failed: %v", err)
		http.Error(w, "could not load stats", http.StatusInternalServerError)
		return
	}
	recent, err := db.RecentGames(r.Context(), h.sqlDB, user.ID, recentGamesLimit)
	if err != nil {
		log.Printf("web: recent games failed: %v", err)
		http.Error(w, "could not load history", http.StatusInternalServerError)
		return
	}

	if err := templates.ExecuteTemplate(w, "stats", newStatsView(user.Email, stats, recent)); err != nil {
		log.Printf("web: render stats: %v", err)
	}
}

func (h *Handler) createGame(w http.ResponseWriter, r *http.Request) {
	d, ok := game.ParseDifficulty(r.FormValue("difficulty"))
	if !ok {
		http.Error(w, "invalid difficulty", http.StatusBadRequest)
		return
	}

	store, userID, err := h.storeFor(r)
	if err != nil {
		log.Printf("web: authenticate failed: %v", err)
		http.Error(w, "could not authenticate request", http.StatusInternalServerError)
		return
	}

	var p sudoku.Puzzle
	if userID != 0 {
		p, err = db.RandomPuzzleForUser(r.Context(), h.sqlDB, d, userID)
	} else {
		p, err = h.puzzles(r.Context(), d)
	}
	if err != nil {
		log.Printf("web: puzzle lookup failed: %v", err)
		http.Error(w, "could not find a puzzle right now", http.StatusInternalServerError)
		return
	}

	g, err := store.Create(r.Context(), p)
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
	store, _, err := h.storeFor(r)
	if err != nil {
		log.Printf("web: authenticate failed: %v", err)
		http.Error(w, "could not authenticate request", http.StatusInternalServerError)
		return
	}
	g, err := store.Get(r.Context(), id)
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

	store, _, err := h.storeFor(r)
	if err != nil {
		log.Printf("web: authenticate failed: %v", err)
		http.Error(w, "could not authenticate request", http.StatusInternalServerError)
		return
	}
	g, err := store.ApplyMove(r.Context(), id, row, col, value)
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

// newStateCookie builds the anti-CSRF OAuth state cookie (or, with
// maxAge -1 and value "", the cookie that clears it). SameSite=Lax
// (not Strict) is required here: the state cookie must survive the
// top-level, cross-site GET redirect back from Google's consent
// screen, which SameSite=Strict would block, dropping the cookie and
// failing every login. Secure is h.secureCookies, not hardcoded true,
// so local HTTP development keeps working — see the Handler.
// secureCookies field doc.
func (h *Handler) newStateCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// newSessionCookie builds the session cookie (or, with maxAge -1 and
// value "", the cookie that clears it on logout). Same SameSite/Secure
// reasoning as newStateCookie.
func (h *Handler) newSessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     auth.CookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

func (h *Handler) googleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := auth.RandomToken(16)
	if err != nil {
		log.Printf("web: generate oauth state: %v", err)
		http.Error(w, "could not start login", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.newStateCookie(state, 600)) // 10 minutes: long enough for a login round trip, short-lived by design
	http.Redirect(w, r, h.auth.LoginURL(state), http.StatusFound)
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, h.newStateCookie("", -1))

	token, err := h.auth.HandleCallback(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		log.Printf("web: oauth callback: %v", err)
		http.Error(w, "login failed", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, h.newSessionCookie(token, sessionCookieMaxAge))
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), auth.FromRequest(r)); err != nil {
		log.Printf("web: logout: %v", err)
	}
	http.SetCookie(w, h.newSessionCookie("", -1))
	http.Redirect(w, r, "/", http.StatusFound)
}
