package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/pricespy/backend/api"
	"github.com/pricespy/backend/db"
	appkafka "github.com/pricespy/backend/kafka"
	"github.com/pricespy/backend/simulator"
)

func TestAPI(t *testing.T) {
	if os.Getenv("POSTGRES_HOST") == "" {
		_ = godotenv.Load("../.env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// ── Connect DB ──────────────────────────────────────────────────
	if err := db.Connect(ctx); err != nil {
		t.Skipf("skipping API test (no postgres available): %v", err)
	}
	defer db.Close()

	// Connect Kafka Producer (requires Kafka to be running)
	producer := appkafka.NewProducer()
	defer producer.Close()

	sim := simulator.New(producer)
	r := api.NewRouter(producer, sim)

	// 1. Test GET /health
	t.Run("GET /health", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/health", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
		}
	})

	// 2. Test POST /api/products
	t.Run("POST /api/products", func(t *testing.T) {
		_, _ = db.Pool.Exec(ctx, "DELETE FROM products WHERE url = $1", "https://www.amazon.in/dp/B0BY8MCQ9S")

		reqBody := map[string]string{
			"url": "https://www.amazon.in/dp/B0BY8MCQ9S",
		}
		bodyJSON, _ := json.Marshal(reqBody)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/products", bytes.NewBuffer(bodyJSON))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("expected status 201 Created, got %d. Body: %s", w.Code, w.Body.String())
		}
	})
}
