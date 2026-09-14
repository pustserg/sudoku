// Command genpuzzles generates and rates the offline puzzle pool and
// bulk-loads it into the puzzles table.
package main

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"runtime"
	"sync"
	"time"

	"github.com/pustserg/sudoku/internal/config"
	"github.com/pustserg/sudoku/internal/db"
	"github.com/pustserg/sudoku/internal/sudoku"
)

func main() {
	easy := flag.Int("easy", 50000, "number of Easy puzzles to generate")
	medium := flag.Int("medium", 50000, "number of Medium puzzles to generate")
	hard := flag.Int("hard", 50000, "number of Hard puzzles to generate")
	expert := flag.Int("expert", 50000, "number of Expert puzzles to generate")
	workers := flag.Int("workers", runtime.NumCPU(), "number of concurrent generator workers")
	batchSize := flag.Int("batch-size", 500, "puzzles buffered per batch insert")
	databaseURL := flag.String("database-url", "", "Postgres connection string (default: DATABASE_URL env var)")
	flag.Parse()

	if *workers < 1 {
		log.Fatalf("--workers must be at least 1, got %d", *workers)
	}

	dsn := *databaseURL
	if dsn == "" {
		dsn = config.Load().DatabaseURL
	}
	if dsn == "" {
		log.Fatal("no database URL: pass --database-url or set DATABASE_URL")
	}

	sqlDB, err := db.Open(dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer sqlDB.Close()

	if err := checkPuzzlesTableExists(sqlDB); err != nil {
		log.Fatalf("%v (run: go run ./cmd/migrate up)", err)
	}

	jobs := buildJobs(*easy, *medium, *hard, *expert)
	total := len(jobs)
	log.Printf("generating %d puzzles (easy=%d medium=%d hard=%d expert=%d) with %d workers",
		total, *easy, *medium, *hard, *expert, *workers)

	jobCh := make(chan sudoku.Difficulty)
	resultCh := make(chan sudoku.Puzzle)

	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(randomSeed(), randomSeed()))
			for d := range jobCh {
				resultCh <- sudoku.Generate(d, rng)
			}
		}()
	}

	go func() {
		for _, d := range jobs {
			jobCh <- d
		}
		close(jobCh)
	}()

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	ctx := context.Background()
	start := time.Now()
	inserted := 0
	batch := make([]sudoku.Puzzle, 0, *batchSize)
	for p := range resultCh {
		batch = append(batch, p)
		if len(batch) >= *batchSize {
			if err := db.InsertPuzzles(ctx, sqlDB, batch); err != nil {
				log.Fatalf("insert batch: %v", err)
			}
			inserted += len(batch)
			batch = batch[:0]
			logProgress(inserted, total, start)
		}
	}
	if len(batch) > 0 {
		if err := db.InsertPuzzles(ctx, sqlDB, batch); err != nil {
			log.Fatalf("insert final batch: %v", err)
		}
		inserted += len(batch)
	}

	log.Printf("done: inserted %d/%d puzzles in %s", inserted, total, time.Since(start))
}

// logProgress logs a progress line every 1000 puzzles inserted, and
// always on the final batch.
func logProgress(inserted, total int, start time.Time) {
	if inserted%1000 != 0 && inserted != total {
		return
	}
	elapsed := time.Since(start)
	log.Printf("inserted %d/%d puzzles (%.1f/sec)", inserted, total, float64(inserted)/elapsed.Seconds())
}

// randomSeed returns a cryptographically random uint64, used to seed each
// worker's independent *rand.Rand so workers never retrace each other's
// generation sequence.
func randomSeed() uint64 {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		log.Fatalf("read random seed: %v", err)
	}
	return binary.BigEndian.Uint64(b[:])
}

// checkPuzzlesTableExists returns an error if the puzzles table doesn't
// exist yet (migrations haven't been applied).
func checkPuzzlesTableExists(sqlDB *sql.DB) error {
	if _, err := sqlDB.Exec("SELECT 1 FROM puzzles LIMIT 0"); err != nil {
		return fmt.Errorf("puzzles table not found: %w", err)
	}
	return nil
}
