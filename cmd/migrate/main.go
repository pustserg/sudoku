// Command migrate applies the migrations in migrations/ to the database
// named by DATABASE_URL, via the golang-migrate library.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/pustserg/sudoku/internal/config"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 || (args[0] != "up" && args[0] != "down") {
		log.Fatal("usage: migrate <up|down>")
	}
	direction := args[0]

	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is not set")
	}

	m, err := migrate.New("file://migrations", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("create migrator: %v", err)
	}

	if direction == "up" {
		err = m.Up()
	} else {
		err = m.Down()
	}
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		log.Fatalf("migrate %s: %v", direction, err)
	}

	version, dirty, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		log.Fatalf("read migration version: %v", err)
	}
	fmt.Printf("migration version: %d (dirty=%v)\n", version, dirty)
}
