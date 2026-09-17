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
	if *batchSize < 1 {
		log.Fatalf("--batch-size must be at least 1, got %d", *batchSize)
	}
	if *easy < 0 {
		log.Fatalf("--easy must be at least 0, got %d", *easy)
	}
	if *medium < 0 {
		log.Fatalf("--medium must be at least 0, got %d", *medium)
	}
	if *hard < 0 {
		log.Fatalf("--hard must be at least 0, got %d", *hard)
	}
	if *expert < 0 {
		log.Fatalf("--expert must be at least 0, got %d", *expert)
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
	var attempted, inserted int64
	batch := make([]sudoku.Puzzle, 0, *batchSize)
	for p := range resultCh {
		batch = append(batch, p)
		if len(batch) >= *batchSize {
			n, err := db.InsertPuzzles(ctx, sqlDB, batch)
			if err != nil {
				log.Fatalf("insert batch: %v", err)
			}
			attempted += int64(len(batch))
			inserted += n
			batch = batch[:0]
			logProgress(inserted, attempted, int64(total), start)
		}
	}
	if len(batch) > 0 {
		n, err := db.InsertPuzzles(ctx, sqlDB, batch)
		if err != nil {
			log.Fatalf("insert final batch: %v", err)
		}
		attempted += int64(len(batch))
		inserted += n
		logProgress(inserted, attempted, int64(total), start)
	}

	log.Printf("done: inserted %d of %d attempted puzzles in %s", inserted, attempted, time.Since(start))
}

// logProgress logs a progress line after every batch insert, reporting
// both how many puzzles have been attempted (generated and submitted
// for insertion) and how many rows were actually inserted (attempted
// minus any cross-worker duplicate givens that were skipped).
func logProgress(inserted, attempted, total int64, start time.Time) {
	elapsed := time.Since(start)
	log.Printf("inserted %d of %d attempted (%d/%d total) (%.1f/sec)",
		inserted, attempted, attempted, total, float64(attempted)/elapsed.Seconds())
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
