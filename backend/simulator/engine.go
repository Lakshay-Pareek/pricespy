package simulator

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	appkafka "github.com/pricespy/backend/kafka"
)

// ─────────────────────────────────────────
// Simulator — the core price simulation engine
// ─────────────────────────────────────────

// Simulator generates realistic price data and publishes to Kafka.
type Simulator struct {
	producer *appkafka.Producer

	// Active events (flash sale, out-of-stock) keyed by product ID
	events sync.Map // map[string]*ActiveEvent

	// Last known price for each product (used for competitor effect + drift)
	lastPrices sync.Map // map[string]float64

	// Product configs keyed by product ID
	configs sync.Map // map[string]*SimulatorConfig

	// Competitor drops pending for next tick: map[productID]dropAmount
	competitorDrops sync.Map // map[string]float64

	mu sync.Mutex
}

// New creates a new Simulator instance.
func New(producer *appkafka.Producer) *Simulator {
	return &Simulator{
		producer: producer,
	}
}

// RegisterProduct associates a SimulatorConfig with a product ID.
func (s *Simulator) RegisterProduct(productID string, cfg *SimulatorConfig) {
	s.configs.Store(productID, cfg)
	s.lastPrices.Store(productID, cfg.BasePrice)
}

// SetLastPrice updates the last known price for a product.
func (s *Simulator) SetLastPrice(productID string, price float64) {
	s.lastPrices.Store(productID, price)
}

// ─────────────────────────────────────────
// Core price generation (all 6 rules)
// ─────────────────────────────────────────

// GeneratePrice computes the next price for a product given its config,
// current price, and a timestamp. This is a pure function used for both
// live ticks and historical backfill.
func (s *Simulator) GeneratePrice(cfg *SimulatorConfig, currentPrice float64, ts time.Time) (float64, bool) {
	inStock := true

	// ── Check active events first ──────────────────────────────────
	// (Events are checked by the caller via ApplyEvents)

	// ── Rule 1: Base Drift (random walk with mean reversion) ───────
	vol := cfg.Volatility.Percent() * cfg.BasePrice
	drift := (rand.Float64()*2 - 1) * vol                  // random(-vol, +vol)
	meanReversion := 0.1 * (cfg.BasePrice - currentPrice)   // pull toward base
	newPrice := currentPrice + drift + meanReversion

	// ── Rule 2: Time-of-Day Pattern ────────────────────────────────
	// Prices slightly lower between 10pm-6am IST (midnight deals)
	ist, _ := time.LoadLocation("Asia/Kolkata")
	hour := ts.In(ist).Hour()
	if hour >= 22 || hour < 6 {
		newPrice *= 0.98 // -2% midnight discount
	}

	// ── Rule 3: Weekend Effect ─────────────────────────────────────
	weekday := ts.In(ist).Weekday()
	switch weekday {
	case time.Saturday:
		// -3% to -5% weekend discount
		discount := 0.03 + rand.Float64()*0.02
		newPrice *= (1 - discount)
	case time.Sunday:
		discount := 0.03 + rand.Float64()*0.02
		newPrice *= (1 - discount)
	case time.Monday:
		// +2% Monday snap-back
		newPrice *= 1.02
	}

	// ── Clamp price to reasonable bounds ────────────────────────────
	minPrice := cfg.BasePrice * 0.50 // never below 50% of base
	maxPrice := cfg.BasePrice * 1.50 // never above 150% of base
	newPrice = math.Max(minPrice, math.Min(maxPrice, newPrice))

	// Round to 2 decimal places
	newPrice = math.Round(newPrice*100) / 100

	return newPrice, inStock
}

// ApplyEvents modifies price and stock status based on any active events.
func (s *Simulator) ApplyEvents(productID string, price float64, now time.Time) (float64, bool) {
	inStock := true

	raw, ok := s.events.Load(productID)
	if !ok {
		return price, inStock
	}

	event := raw.(*ActiveEvent)

	// Clean up expired events
	if event.IsExpired(now) {
		s.events.Delete(productID)
		return price, inStock
	}

	switch event.Type {
	case EventFlashSale:
		if event.IsActive(now) {
			// Apply the full flash sale drop
			price = event.PreEventPrice * (1 - event.DropPercent)
		} else if event.IsRecovering(now) {
			// Gradually recover: interpolate between dropped price and pre-event price
			droppedPrice := event.PreEventPrice * (1 - event.DropPercent)
			progress := event.RecoveryProgress(now)
			price = droppedPrice + (event.PreEventPrice-droppedPrice)*progress
		}
		price = math.Round(price*100) / 100

	case EventOutOfStock:
		if event.IsActive(now) {
			// Out of stock: price +8%, not available
			price = event.PreEventPrice * 1.08
			price = math.Round(price*100) / 100
			inStock = false
		}
		// After event duration, price and stock revert automatically
	}

	return price, inStock
}

// ApplyCompetitorEffect checks if there's a pending competitor drop
// for this product and applies it.
func (s *Simulator) ApplyCompetitorEffect(productID string, price float64) float64 {
	raw, ok := s.competitorDrops.LoadAndDelete(productID)
	if !ok {
		return price
	}
	drop := raw.(float64)
	newPrice := price - drop
	if newPrice < 0 {
		newPrice = price * 0.95
	}
	return math.Round(newPrice*100) / 100
}

// ─────────────────────────────────────────
// Event triggers
// ─────────────────────────────────────────

// TriggerFlashSale starts a flash sale event on a product.
// Price drops 15-25% instantly, stays low for 2 hours, recovers over 4 hours.
func (s *Simulator) TriggerFlashSale(productID string) error {
	currentPrice := s.getLastPrice(productID)
	if currentPrice == 0 {
		return fmt.Errorf("product %s not found in simulator", productID)
	}

	dropPercent := 0.15 + rand.Float64()*0.10 // 15-25%

	event := &ActiveEvent{
		Type:             EventFlashSale,
		StartedAt:        time.Now(),
		Duration:         2 * time.Hour,
		RecoveryDuration: 4 * time.Hour,
		DropPercent:      dropPercent,
		PreEventPrice:    currentPrice,
	}

	s.events.Store(productID, event)
	log.Printf("[simulator] ⚡ flash sale triggered for product %s: -%.0f%% (₹%.2f → ₹%.2f)",
		productID, dropPercent*100, currentPrice, currentPrice*(1-dropPercent))

	return nil
}

// TriggerOutOfStock starts an out-of-stock event on a product.
// Sets in_stock=false, price +8%, reverts after 1 hour.
func (s *Simulator) TriggerOutOfStock(productID string) error {
	currentPrice := s.getLastPrice(productID)
	if currentPrice == 0 {
		return fmt.Errorf("product %s not found in simulator", productID)
	}

	event := &ActiveEvent{
		Type:             EventOutOfStock,
		StartedAt:        time.Now(),
		Duration:         1 * time.Hour,
		RecoveryDuration: 0, // instant recovery
		DropPercent:      -0.08, // negative = price increase
		PreEventPrice:    currentPrice,
	}

	s.events.Store(productID, event)
	log.Printf("[simulator] 📦 out-of-stock triggered for product %s: price +8%% (₹%.2f → ₹%.2f)",
		productID, currentPrice, currentPrice*1.08)

	return nil
}

// TriggerCompetitorDrop triggers a price drop on one product and queues
// a 50% mirrored drop on all same-category competitors.
func (s *Simulator) TriggerCompetitorDrop(productID string) error {
	// Find the config for this product
	rawCfg, ok := s.configs.Load(productID)
	if !ok {
		return fmt.Errorf("product %s not found in simulator configs", productID)
	}
	cfg := rawCfg.(*SimulatorConfig)

	// Drop this product's price by 5-10%
	currentPrice := s.getLastPrice(productID)
	dropAmount := currentPrice * (0.05 + rand.Float64()*0.05)

	// Queue 50% of this drop for same-category competitors
	s.configs.Range(func(key, value any) bool {
		otherID := key.(string)
		otherCfg := value.(*SimulatorConfig)
		if otherID != productID && otherCfg.Category == cfg.Category {
			competitorDrop := dropAmount * 0.5
			s.competitorDrops.Store(otherID, competitorDrop)
			log.Printf("[simulator] 🔄 queued competitor drop for %s: -₹%.2f (50%% of ₹%.2f)",
				otherID, competitorDrop, dropAmount)
		}
		return true
	})

	// Apply the drop to the triggering product immediately
	newPrice := math.Round((currentPrice-dropAmount)*100) / 100
	s.lastPrices.Store(productID, newPrice)

	log.Printf("[simulator] 🔄 competitor drop triggered from %s: -₹%.2f", productID, dropAmount)
	return nil
}

// ─────────────────────────────────────────
// Tick — generate one round of prices for all products
// ─────────────────────────────────────────

// Tick generates a new price for every registered product and publishes
// PriceScrapedMsg to Kafka. Called by the background ticker and fast-forward.
func (s *Simulator) Tick(ctx context.Context, ts time.Time) {
	s.configs.Range(func(key, value any) bool {
		productID := key.(string)
		cfg := value.(*SimulatorConfig)

		currentPrice := s.getLastPrice(productID)

		// Generate base price with drift + time effects
		newPrice, inStock := s.GeneratePrice(cfg, currentPrice, ts)

		// Apply competitor effect (if any pending)
		newPrice = s.ApplyCompetitorEffect(productID, newPrice)

		// Apply active events (flash sale / OOS)
		newPrice, inStock = s.ApplyEvents(productID, newPrice, ts)

		// Update last known price
		s.lastPrices.Store(productID, newPrice)

		// Publish to Kafka (same as a real scraper would)
		msg := appkafka.PriceScrapedMsg{
			ProductID: productID,
			URL:       cfg.URL,
			Platform:  cfg.Platform,
			Price:     newPrice,
			Currency:  "INR",
			InStock:   inStock,
			ScrapedAt: ts.UTC().Format(time.RFC3339),
		}

		if err := s.producer.PublishPriceScraped(ctx, msg); err != nil {
			log.Printf("[simulator] failed to publish price for %s: %v", productID, err)
		}

		return true
	})
}

// ─────────────────────────────────────────
// Fast-forward — generate N hours of data instantly
// ─────────────────────────────────────────

// FastForward generates `hours` worth of price data (one tick per 30 min)
// and publishes each to Kafka. Returns the number of ticks generated.
func (s *Simulator) FastForward(ctx context.Context, hours int) int {
	ticksPerHour := 2 // every 30 minutes
	totalTicks := hours * ticksPerHour

	startTime := time.Now()

	log.Printf("[simulator] ⏩ fast-forwarding %d hours (%d ticks)...", hours, totalTicks)

	for i := 0; i < totalTicks; i++ {
		ts := startTime.Add(time.Duration(i) * 30 * time.Minute)
		s.Tick(ctx, ts)
	}

	log.Printf("[simulator] ⏩ fast-forward complete: %d ticks generated", totalTicks)
	return totalTicks
}

// ─────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────

func (s *Simulator) getLastPrice(productID string) float64 {
	raw, ok := s.lastPrices.Load(productID)
	if !ok {
		return 0
	}
	return raw.(float64)
}

// GetProductIDByName finds the product ID for a named demo product.
// Returns empty string if not found.
func (s *Simulator) GetProductIDByName(name string) string {
	var found string
	s.configs.Range(func(key, value any) bool {
		cfg := value.(*SimulatorConfig)
		if cfg.Name == name {
			found = key.(string)
			return false // stop iteration
		}
		return true
	})
	return found
}

// GetProductIDByURL finds the product ID for a URL.
func (s *Simulator) GetProductIDByURL(url string) string {
	var found string
	s.configs.Range(func(key, value any) bool {
		cfg := value.(*SimulatorConfig)
		if cfg.URL == url {
			found = key.(string)
			return false
		}
		return true
	})
	return found
}

// GenerateHistoricalPrice is like GeneratePrice but uses a seeded random
// source for deterministic historical data generation.
func GenerateHistoricalPrice(cfg *SimulatorConfig, currentPrice float64, ts time.Time, rng *rand.Rand) (float64, bool) {
	inStock := true

	// Rule 1: Base Drift
	vol := cfg.Volatility.Percent() * cfg.BasePrice
	drift := (rng.Float64()*2 - 1) * vol
	meanReversion := 0.1 * (cfg.BasePrice - currentPrice)
	newPrice := currentPrice + drift + meanReversion

	// Rule 2: Time-of-Day
	ist, _ := time.LoadLocation("Asia/Kolkata")
	hour := ts.In(ist).Hour()
	if hour >= 22 || hour < 6 {
		newPrice *= 0.98
	}

	// Rule 3: Weekend Effect
	weekday := ts.In(ist).Weekday()
	switch weekday {
	case time.Saturday:
		discount := 0.03 + rng.Float64()*0.02
		newPrice *= (1 - discount)
	case time.Sunday:
		discount := 0.03 + rng.Float64()*0.02
		newPrice *= (1 - discount)
	case time.Monday:
		newPrice *= 1.02
	}

	// Clamp
	minPrice := cfg.BasePrice * 0.50
	maxPrice := cfg.BasePrice * 1.50
	newPrice = math.Max(minPrice, math.Min(maxPrice, newPrice))
	newPrice = math.Round(newPrice*100) / 100

	return newPrice, inStock
}

// FindDemoProductID looks up the UUID of a demo product by name.
// Uses the in-memory config registry first, falling back to DB query.
func (s *Simulator) FindDemoProductID(name string) (uuid.UUID, error) {
	idStr := s.GetProductIDByName(name)
	if idStr == "" {
		return uuid.Nil, fmt.Errorf("demo product %q not found", name)
	}
	return uuid.Parse(idStr)
}
