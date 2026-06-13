package db

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool is the global connection pool — shared across the app
var Pool *pgxpool.Pool

// Connect initialises the pgx connection pool from environment variables.
// Call this once at startup (main.go). Panics if the DB is unreachable.
func Connect(ctx context.Context) error {
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		mustEnv("POSTGRES_USER"),
		mustEnv("POSTGRES_PASSWORD"),
		mustEnv("POSTGRES_HOST"),
		mustEnv("POSTGRES_PORT"),
		mustEnv("POSTGRES_DB"),
	)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("db: parse config: %w", err)
	}

	// Pool tuning
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 1 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("db: create pool: %w", err)
	}

	// Verify connectivity
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping failed: %w", err)
	}

	Pool = pool
	log.Printf("[db] connected to postgres at %s:%s/%s (pool max=%d)",
		mustEnv("POSTGRES_HOST"),
		mustEnv("POSTGRES_PORT"),
		mustEnv("POSTGRES_DB"),
		cfg.MaxConns,
	)

	// Run schema migration to add price_source if not exists
	InitDB(ctx)

	return nil
}

// Close gracefully shuts down the pool. Call this in a defer after Connect.
func Close() {
	if Pool != nil {
		Pool.Close()
		log.Println("[db] connection pool closed")
	}
}

// mustEnv reads an env var and panics if it is missing.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("[db] missing required environment variable: %s", key)
	}
	return v
}
