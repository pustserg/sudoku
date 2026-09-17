package db

import (
	"context"
	"database/sql"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Open opens a connection pool to the Postgres database at databaseURL,
// via the pgx stdlib driver, and verifies connectivity with a ping
// before returning, so callers fail fast on bad configuration rather
// than on the first query.
func Open(databaseURL string) (*sql.DB, error) {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return sqlDB, nil
}
