package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/pustserg/sudoku/internal/api"
	"github.com/pustserg/sudoku/internal/auth"
	"github.com/pustserg/sudoku/internal/config"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/game"
	"github.com/pustserg/sudoku/internal/sudoku"
	"github.com/pustserg/sudoku/internal/web"
)

func main() {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	if err := runMigrations(cfg.DatabaseURL); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	sqlDB, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" || cfg.GoogleRedirectURL == "" {
		log.Fatal("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, and GOOGLE_REDIRECT_URL must all be set")
	}
	authSvc := auth.NewService(sqlDB, cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL)

	anonStore := game.NewStore()
	puzzleLookup := game.PuzzleLookup(func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		return db.RandomPuzzle(ctx, sqlDB, d)
	})

	// secureCookies mirrors whether this deployment is actually served
	// over HTTPS: true wherever GoogleRedirectURL is https:// (every
	// real deployment), false for local http://localhost dev, where a
	// Secure cookie would never be sent back and login would break.
	secureCookies := strings.HasPrefix(cfg.GoogleRedirectURL, "https://")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler)
	api.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB).Register(mux)
	web.NewHandler(anonStore, puzzleLookup, authSvc, sqlDB, secureCookies).Register(mux)

	addr := ":" + cfg.Port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// runMigrations applies every pending migration in migrations/ to
// databaseURL, so a fresh checkout only needs `go run ./cmd/server` —
// no separate `go run ./cmd/migrate up` step. A real failure (bad SQL,
// dirty migration state) still fails startup fast, same as an invalid
// DATABASE_URL.
func runMigrations(databaseURL string) error {
	m, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}
	return nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
