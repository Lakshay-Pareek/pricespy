package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/pricespy/backend/api"
	"github.com/pricespy/backend/db"
	appkafka "github.com/pricespy/backend/kafka"
	"github.com/pricespy/backend/simulator"
)

func main() {
	// ── Load .env ──────────────────────────────────────────────────
	if err := godotenv.Load(); err != nil {
		log.Println("[main] no .env file found — reading from system environment")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// ── Root context (cancelled on SIGINT / SIGTERM) ────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Database ────────────────────────────────────────────────────
	if err := db.Connect(ctx); err != nil {
		log.Fatalf("[main] database connection failed: %v", err)
	}
	defer db.Close()

	// ── Kafka Producer ──────────────────────────────────────────────
	producer := appkafka.NewProducer()
	defer producer.Close()

	// ── Kafka Consumer (runs in background goroutine) ───────────────
	consumer := appkafka.NewConsumer(producer)
	defer consumer.Close()

	go consumer.Start(ctx)

	// ── Price Simulator ─────────────────────────────────────────────
	sim := simulator.New(producer)

	// Seed demo products + 30 days of historical price data
	if err := simulator.SeedDemoProducts(ctx, sim); err != nil {
		log.Printf("[main] ⚠ demo product seeding had errors: %v", err)
	}

	// Start background price generation ticker
	go sim.StartTicker(ctx)

	// ── Gin REST API Router ─────────────────────────────────────────
	router := api.NewRouter(producer, sim)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: router,
	}

	// Start API server in a goroutine
	go func() {
		log.Printf("[main] PriceSpy API ready on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[main] Server failed to start: %v", err)
		}
	}()

	// Block until OS signal
	<-ctx.Done()
	log.Println("[main] shutting down gracefully...")

	// Gracefully shutdown the server with 5-second timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[main] Server forced to shutdown: %v", err)
	}
	log.Println("[main] Server exited cleanly")
}
