package simulator

import "time"

// ─────────────────────────────────────────
// Volatility + Category enums
// ─────────────────────────────────────────

type Volatility string

const (
	VolatilityLow    Volatility = "low"
	VolatilityMedium Volatility = "medium"
	VolatilityHigh   Volatility = "high"
)

// VolatilityPercent returns the max % swing per tick for each level.
func (v Volatility) Percent() float64 {
	switch v {
	case VolatilityHigh:
		return 0.03 // ±3%
	case VolatilityMedium:
		return 0.02 // ±2%
	default:
		return 0.01 // ±1%
	}
}

type Category string

const (
	CategoryElectronics Category = "electronics"
	CategoryFashion     Category = "fashion"
	CategoryAppliances  Category = "appliances"
)

// ─────────────────────────────────────────
// SimulatorConfig — per-product config
// ─────────────────────────────────────────

type SimulatorConfig struct {
	Name       string
	URL        string
	Platform   string // "amazon" or "flipkart"
	Category   Category
	BasePrice  float64
	Volatility Volatility
}

// ─────────────────────────────────────────
// Demo products — pre-seeded on startup
// ─────────────────────────────────────────

var DemoProducts = []SimulatorConfig{
	{
		Name:       "iPhone 15",
		URL:        "https://www.amazon.in/dp/B0CHX1W1XY",
		Platform:   "amazon",
		Category:   CategoryElectronics,
		BasePrice:  79999,
		Volatility: VolatilityHigh,
	},
	{
		Name:       "Samsung Galaxy S24",
		URL:        "https://www.flipkart.com/samsung-galaxy-s24/p/itm123456",
		Platform:   "flipkart",
		Category:   CategoryElectronics,
		BasePrice:  74999,
		Volatility: VolatilityHigh,
	},
	{
		Name:       "Sony WH-1000XM5 Headphones",
		URL:        "https://www.amazon.in/dp/B0BX2L8PBL",
		Platform:   "amazon",
		Category:   CategoryElectronics,
		BasePrice:  29999,
		Volatility: VolatilityMedium,
	},
	{
		Name:       "Nike Air Max",
		URL:        "https://www.flipkart.com/nike-air-max/p/itm789012",
		Platform:   "flipkart",
		Category:   CategoryFashion,
		BasePrice:  12999,
		Volatility: VolatilityLow,
	},
	{
		Name:       "LG 55\" 4K TV",
		URL:        "https://www.amazon.in/dp/B0BVZN3LYZ",
		Platform:   "amazon",
		Category:   CategoryAppliances,
		BasePrice:  54999,
		Volatility: VolatilityMedium,
	},
}

// ─────────────────────────────────────────
// Event types for flash sales / OOS
// ─────────────────────────────────────────

type EventType string

const (
	EventFlashSale  EventType = "flash_sale"
	EventOutOfStock EventType = "out_of_stock"
)

// ActiveEvent represents a time-limited pricing event on a product.
type ActiveEvent struct {
	Type      EventType
	StartedAt time.Time
	Duration  time.Duration // how long the event stays active
	// For flash sale: the recovery period after the active phase
	RecoveryDuration time.Duration
	// The price multiplier applied during the event
	DropPercent float64
	// The original price before event was applied
	PreEventPrice float64
}

// IsActive returns true if the event is still in its active phase.
func (e *ActiveEvent) IsActive(now time.Time) bool {
	return now.Before(e.StartedAt.Add(e.Duration))
}

// IsRecovering returns true if the event is in recovery phase (flash sale only).
func (e *ActiveEvent) IsRecovering(now time.Time) bool {
	activeEnd := e.StartedAt.Add(e.Duration)
	recoveryEnd := activeEnd.Add(e.RecoveryDuration)
	return now.After(activeEnd) && now.Before(recoveryEnd)
}

// IsExpired returns true if the event has fully expired.
func (e *ActiveEvent) IsExpired(now time.Time) bool {
	totalDuration := e.Duration + e.RecoveryDuration
	return now.After(e.StartedAt.Add(totalDuration))
}

// RecoveryProgress returns 0.0-1.0 representing how far through recovery we are.
func (e *ActiveEvent) RecoveryProgress(now time.Time) float64 {
	activeEnd := e.StartedAt.Add(e.Duration)
	if now.Before(activeEnd) {
		return 0
	}
	elapsed := now.Sub(activeEnd).Seconds()
	total := e.RecoveryDuration.Seconds()
	if total == 0 {
		return 1
	}
	progress := elapsed / total
	if progress > 1 {
		return 1
	}
	return progress
}
