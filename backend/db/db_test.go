package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/pricespy/backend/db"
)

// TestDB runs a quick round-trip against the real Docker Postgres.
// Run with: go test ./db/... -v -run TestDB
// Requires Docker Compose stack to be running.
func TestDB(t *testing.T) {
	if os.Getenv("POSTGRES_HOST") == "" {
		_ = godotenv.Load("../.env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// ── Connect ───────────────────────────────────────────────────
	if err := db.Connect(ctx); err != nil {
		t.Skipf("skipping DB test (no postgres available): %v", err)
	}
	defer db.Close()

	// ── Insert product ────────────────────────────────────────────
	product, err := db.InsertProduct(ctx,
		"https://www.amazon.in/dp/TEST123",
		"Test Product",
		db.PlatformAmazon,
		"simulated",
	)
	if err != nil {
		t.Fatalf("InsertProduct: %v", err)
	}
	t.Logf("Inserted product: %s (%s)", product.Name, product.ID)

	// ── Insert price history ──────────────────────────────────────
	ph, err := db.InsertPriceHistory(ctx, product.ID, 1499.00, "INR", true)
	if err != nil {
		t.Fatalf("InsertPriceHistory: %v", err)
	}
	t.Logf("Inserted price: ₹%.2f at %s", ph.Price, ph.ScrapedAt)

	// ── Get latest price ──────────────────────────────────────────
	latest, err := db.GetLatestPrice(ctx, product.ID)
	if err != nil {
		t.Fatalf("GetLatestPrice: %v", err)
	}
	if latest.Price != 1499.00 {
		t.Errorf("expected price 1499.00, got %.2f", latest.Price)
	}

	// ── Get history (last 7d) ─────────────────────────────────────
	history, err := db.GetPriceHistory(ctx, product.ID, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("GetPriceHistory: %v", err)
	}
	if len(history) == 0 {
		t.Error("expected at least 1 price history record")
	}
	t.Logf("History records: %d", len(history))

	// ── Get all products ──────────────────────────────────────────
	products, err := db.GetProducts(ctx)
	if err != nil {
		t.Fatalf("GetProducts: %v", err)
	}
	t.Logf("Total products: %d", len(products))

	// ── Insert + trigger alert ────────────────────────────────────
	alert, err := db.InsertAlert(ctx, product.ID, 1200.00)
	if err != nil {
		t.Fatalf("InsertAlert: %v", err)
	}
	t.Logf("Alert created: target ₹%.2f", alert.TargetPrice)

	if err := db.TriggerAlert(ctx, alert.ID); err != nil {
		t.Fatalf("TriggerAlert: %v", err)
	}
	t.Log("Alert triggered ✓")
}
