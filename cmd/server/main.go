package main

import (
	"context"
	"log"
	"net/http"

	"github.com/pustserg/sudoku/internal/api"
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

	sqlDB, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	store := game.NewStore()
	puzzleLookup := game.PuzzleLookup(func(ctx context.Context, d sudoku.Difficulty) (sudoku.Puzzle, error) {
		return db.RandomPuzzle(ctx, sqlDB, d)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler)
	api.NewHandler(store, puzzleLookup).Register(mux)
	web.NewHandler(store, puzzleLookup).Register(mux)

	addr := ":" + cfg.Port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}
