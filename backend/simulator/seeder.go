package simulator

import (
	"context"
	"log"
	"math/rand"
	"time"

	"github.com/pricespy/backend/db"
)

// SeedDemoProducts inserts the 5 demo products into the database and
// generates 30 days of historical price data for each.
// Skips products that already have sufficient history.
func SeedDemoProducts(ctx context.Context, sim *Simulator) error {
	log.Println("[simulator] seeding demo products...")

	for _, cfg := range DemoProducts {
		// Guard: skip invalid configs
		if cfg.Name == "" || cfg.BasePrice == 0 {
			log.Printf("[simulator] ⚠ skipping invalid product config (empty name or zero price): %+v", cfg)
			continue
		}

		// Upsert the product
		product, err := db.InsertProduct(ctx, cfg.URL, cfg.Name, db.Platform(cfg.Platform), "simulated")
		if err != nil {
			log.Printf("[simulator] ⚠ failed to insert product %s: %v", cfg.Name, err)
			continue
		}

		productID := product.ID.String()

		// Register this product in the simulator
		cfgCopy := cfg // avoid closure capture issues
		sim.RegisterProduct(productID, &cfgCopy)

		log.Printf("[simulator] ✓ product registered: %s (%s) — %s",
			cfg.Name, productID, cfg.Platform)

		// Check if history already exists
		since := time.Now().Add(-30 * 24 * time.Hour)
		history, err := db.GetPriceHistory(ctx, product.ID, since)
		if err != nil {
			log.Printf("[simulator] ⚠ failed to check history for %s: %v", cfg.Name, err)
			continue
		}

		// If we already have significant history (>100 entries), skip backfill
		if len(history) > 100 {
			log.Printf("[simulator] ⏭ product %s already has %d history entries — skipping backfill",
				cfg.Name, len(history))

			// Set last price from most recent entry
			if len(history) > 0 {
				lastEntry := history[len(history)-1]
				sim.SetLastPrice(productID, lastEntry.Price)
			}
			continue
		}

		// Generate 30 days of historical data
		if err := seedProductHistory(ctx, sim, productID, &cfg); err != nil {
			log.Printf("[simulator] ⚠ failed to seed history for %s: %v", cfg.Name, err)
		}
	}

	log.Println("[simulator] ✅ demo product seeding complete")
	return nil
}

// seedProductHistory generates 30 days of price history for a single product.
// Writes directly to Postgres (bypasses Kafka for speed).
func seedProductHistory(ctx context.Context, sim *Simulator, productID string, cfg *SimulatorConfig) error {
	// Use a deterministic seed per product for consistent historical data
	seed := int64(0)
	for _, c := range cfg.Name {
		seed += int64(c)
	}
	rng := rand.New(rand.NewSource(seed))

	now := time.Now()
	startTime := now.Add(-30 * 24 * time.Hour) // 30 days ago
	interval := 30 * time.Minute                // one data point every 30 minutes
	totalTicks := int(30 * 24 * 60 / 30)        // 30 days * 48 per day = 1440

	currentPrice := cfg.BasePrice
	inserted := 0

	log.Printf("[simulator] 📊 seeding %d historical prices for %s...", totalTicks, cfg.Name)

	for i := 0; i < totalTicks; i++ {
		ts := startTime.Add(time.Duration(i) * interval)

		// Generate price using seeded RNG for deterministic history
		newPrice, inStock := GenerateHistoricalPrice(cfg, currentPrice, ts, rng)
		currentPrice = newPrice

		// Write directly to Postgres (bypass Kafka for historical data)
		_, err := db.InsertPriceHistoryAt(ctx, productID, newPrice, "INR", inStock, ts)
		if err != nil {
			// Log but don't fail — some entries might have constraint issues
			if inserted == 0 {
				log.Printf("[simulator] ⚠ first insert failed for %s: %v", cfg.Name, err)
			}
			continue
		}
		inserted++
	}

	// Update the simulator's last known price
	sim.SetLastPrice(productID, currentPrice)

	log.Printf("[simulator] ✅ seeded %d/%d historical prices for %s (final: ₹%.2f)",
		inserted, totalTicks, cfg.Name, currentPrice)

	return nil
}
