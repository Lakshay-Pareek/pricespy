package db

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────
// Domain models
// ─────────────────────────────────────────

// Platform represents the e-commerce platform.
type Platform string

const (
	PlatformAmazon   Platform = "amazon"
	PlatformFlipkart Platform = "flipkart"
)

// Product mirrors the products table.
type Product struct {
	ID          uuid.UUID `json:"id"`
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	Platform    Platform  `json:"platform"`
	PriceSource string    `json:"price_source"`
	CreatedAt   time.Time `json:"created_at"`
}

// PriceHistory mirrors the price_history table.
type PriceHistory struct {
	ID        uuid.UUID `json:"id"`
	ProductID uuid.UUID `json:"product_id"`
	Price     float64   `json:"price"`
	Currency  string    `json:"currency"`
	ScrapedAt time.Time `json:"scraped_at"`
	InStock   bool      `json:"in_stock"`
}

// Alert mirrors the alerts table.
type Alert struct {
	ID          uuid.UUID  `json:"id"`
	ProductID   uuid.UUID  `json:"product_id"`
	TargetPrice float64    `json:"target_price"`
	Triggered   bool       `json:"triggered"`
	CreatedAt   time.Time  `json:"created_at"`
	TriggeredAt *time.Time `json:"triggered_at,omitempty"`
}

// PriceStats holds aggregate stats used for buy/wait signals.
type PriceStats struct {
	ProductID  uuid.UUID `json:"product_id"`
	AvgPrice   float64   `json:"avg_price"`
	MinPrice   float64   `json:"min_price"`
	MaxPrice   float64   `json:"max_price"`
	DataPoints int       `json:"data_points"`
}

// ─────────────────────────────────────────
// Products
// ─────────────────────────────────────────

// InsertProduct inserts a new product and returns the created record.
// If the URL already exists, returns the existing record.
// Only updates the name on conflict if the new name is non-empty
// (prevents AddProduct with empty name from wiping seeded product names).
func InsertProduct(ctx context.Context, url, name string, platform Platform, priceSource string) (*Product, error) {
	const q = `
		INSERT INTO products (url, name, platform, price_source)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (url) DO UPDATE
			SET name = CASE
				WHEN EXCLUDED.name != '' THEN EXCLUDED.name
				ELSE products.name
			END,
			price_source = CASE
				WHEN EXCLUDED.price_source != '' THEN EXCLUDED.price_source
				ELSE products.price_source
			END
		RETURNING id, url, name, platform, price_source, created_at
	`
	var p Product
	row := Pool.QueryRow(ctx, q, url, name, platform, priceSource)
	err := row.Scan(&p.ID, &p.URL, &p.Name, &p.Platform, &p.PriceSource, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("db.InsertProduct: %w", err)
	}
	return &p, nil
}

// GetProducts returns all tracked products ordered by creation date desc.
func GetProducts(ctx context.Context) ([]Product, error) {
	const q = `
		SELECT id, url, name, platform, price_source, created_at
		FROM products
		ORDER BY created_at DESC
	`
	rows, err := Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("db.GetProducts: %w", err)
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.URL, &p.Name, &p.Platform, &p.PriceSource, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("db.GetProducts scan: %w", err)
		}
		products = append(products, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db.GetProducts rows: %w", err)
	}
	return products, nil
}

// GetProductByID fetches a single product by UUID.
func GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	const q = `
		SELECT id, url, name, platform, price_source, created_at
		FROM products
		WHERE id = $1
	`
	var p Product
	err := Pool.QueryRow(ctx, q, id).Scan(&p.ID, &p.URL, &p.Name, &p.Platform, &p.PriceSource, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("db.GetProductByID: %w", err)
	}
	return &p, nil
}

// GetProductByURL fetches a product by its URL.
func GetProductByURL(ctx context.Context, url string) (*Product, error) {
	const q = `
		SELECT id, url, name, platform, price_source, created_at
		FROM products
		WHERE url = $1
	`
	var p Product
	err := Pool.QueryRow(ctx, q, url).Scan(&p.ID, &p.URL, &p.Name, &p.Platform, &p.PriceSource, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("db.GetProductByURL: %w", err)
	}
	return &p, nil
}

// ─────────────────────────────────────────
// Price History
// ─────────────────────────────────────────

// InsertPriceHistory records a new price data point for a product.
func InsertPriceHistory(ctx context.Context, productID uuid.UUID, price float64, currency string, inStock bool) (*PriceHistory, error) {
	const q = `
		INSERT INTO price_history (product_id, price, currency, in_stock)
		VALUES ($1, $2, $3, $4)
		RETURNING id, product_id, price, currency, scraped_at, in_stock
	`
	var ph PriceHistory
	err := Pool.QueryRow(ctx, q, productID, price, currency, inStock).
		Scan(&ph.ID, &ph.ProductID, &ph.Price, &ph.Currency, &ph.ScrapedAt, &ph.InStock)
	if err != nil {
		return nil, fmt.Errorf("db.InsertPriceHistory: %w", err)
	}
	return &ph, nil
}

// InsertPriceHistoryAt records a price data point with a specific timestamp.
// Used by the simulator seeder to backfill historical data.
func InsertPriceHistoryAt(ctx context.Context, productIDStr string, price float64, currency string, inStock bool, scrapedAt time.Time) (*PriceHistory, error) {
	productID, err := uuid.Parse(productIDStr)
	if err != nil {
		return nil, fmt.Errorf("db.InsertPriceHistoryAt: invalid product_id %q: %w", productIDStr, err)
	}

	const q = `
		INSERT INTO price_history (product_id, price, currency, in_stock, scraped_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, product_id, price, currency, scraped_at, in_stock
	`
	var ph PriceHistory
	err = Pool.QueryRow(ctx, q, productID, price, currency, inStock, scrapedAt).
		Scan(&ph.ID, &ph.ProductID, &ph.Price, &ph.Currency, &ph.ScrapedAt, &ph.InStock)
	if err != nil {
		return nil, fmt.Errorf("db.InsertPriceHistoryAt: %w", err)
	}
	return &ph, nil
}

// GetPriceHistory returns price history for a product within a time window,
// ordered chronologically (oldest → newest) for charting.
func GetPriceHistory(ctx context.Context, productID uuid.UUID, since time.Time) ([]PriceHistory, error) {
	const q = `
		SELECT id, product_id, price, currency, scraped_at, in_stock
		FROM price_history
		WHERE product_id = $1
		  AND scraped_at >= $2
		ORDER BY scraped_at ASC
	`
	rows, err := Pool.Query(ctx, q, productID, since)
	if err != nil {
		return nil, fmt.Errorf("db.GetPriceHistory: %w", err)
	}
	defer rows.Close()

	var history []PriceHistory
	for rows.Next() {
		var ph PriceHistory
		if err := rows.Scan(&ph.ID, &ph.ProductID, &ph.Price, &ph.Currency, &ph.ScrapedAt, &ph.InStock); err != nil {
			return nil, fmt.Errorf("db.GetPriceHistory scan: %w", err)
		}
		history = append(history, ph)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db.GetPriceHistory rows: %w", err)
	}
	return history, nil
}

// GetPriceHistoryAll returns the complete price history for a product (no time window),
// ordered chronologically (oldest → newest) for charting the ALL range.
func GetPriceHistoryAll(ctx context.Context, productID uuid.UUID) ([]PriceHistory, error) {
	const q = `
		SELECT id, product_id, price, currency, scraped_at, in_stock
		FROM price_history
		WHERE product_id = $1
		ORDER BY scraped_at ASC
	`
	rows, err := Pool.Query(ctx, q, productID)
	if err != nil {
		return nil, fmt.Errorf("db.GetPriceHistoryAll: %w", err)
	}
	defer rows.Close()

	var history []PriceHistory
	for rows.Next() {
		var ph PriceHistory
		if err := rows.Scan(&ph.ID, &ph.ProductID, &ph.Price, &ph.Currency, &ph.ScrapedAt, &ph.InStock); err != nil {
			return nil, fmt.Errorf("db.GetPriceHistoryAll scan: %w", err)
		}
		history = append(history, ph)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db.GetPriceHistoryAll rows: %w", err)
	}
	return history, nil
}

// GetLatestPrice returns the most recent price record for a product.
func GetLatestPrice(ctx context.Context, productID uuid.UUID) (*PriceHistory, error) {
	const q = `
		SELECT id, product_id, price, currency, scraped_at, in_stock
		FROM price_history
		WHERE product_id = $1
		ORDER BY scraped_at DESC
		LIMIT 1
	`
	var ph PriceHistory
	err := Pool.QueryRow(ctx, q, productID).
		Scan(&ph.ID, &ph.ProductID, &ph.Price, &ph.Currency, &ph.ScrapedAt, &ph.InStock)
	if err != nil {
		return nil, fmt.Errorf("db.GetLatestPrice: %w", err)
	}
	return &ph, nil
}

// GetPriceStats7d returns avg/min/max price over the last 7 days for a product.
func GetPriceStats7d(ctx context.Context, productID uuid.UUID) (*PriceStats, error) {
	const q = `
		SELECT
			product_id,
			ROUND(AVG(price)::numeric, 2),
			MIN(price),
			MAX(price),
			COUNT(*)
		FROM price_history
		WHERE product_id = $1
		  AND scraped_at >= NOW() - INTERVAL '7 days'
		GROUP BY product_id
	`
	var s PriceStats
	err := Pool.QueryRow(ctx, q, productID).
		Scan(&s.ProductID, &s.AvgPrice, &s.MinPrice, &s.MaxPrice, &s.DataPoints)
	if err != nil {
		return nil, fmt.Errorf("db.GetPriceStats7d: %w", err)
	}
	return &s, nil
}

// GetPriceStats30d returns avg/min/max price over the last 30 days for a product.
func GetPriceStats30d(ctx context.Context, productID uuid.UUID) (*PriceStats, error) {
	const q = `
		SELECT
			product_id,
			ROUND(AVG(price)::numeric, 2),
			MIN(price),
			MAX(price),
			COUNT(*)
		FROM price_history
		WHERE product_id = $1
		  AND scraped_at >= NOW() - INTERVAL '30 days'
		GROUP BY product_id
	`
	var s PriceStats
	err := Pool.QueryRow(ctx, q, productID).
		Scan(&s.ProductID, &s.AvgPrice, &s.MinPrice, &s.MaxPrice, &s.DataPoints)
	if err != nil {
		return nil, fmt.Errorf("db.GetPriceStats30d: %w", err)
	}
	return &s, nil
}

// IsLowestIn30Days returns true if price is the lowest recorded in the last 30 days.
func IsLowestIn30Days(ctx context.Context, productID uuid.UUID, price float64) (bool, error) {
	const q = `
		SELECT MIN(price)
		FROM price_history
		WHERE product_id = $1
		  AND scraped_at >= NOW() - INTERVAL '30 days'
	`
	var minPrice float64
	err := Pool.QueryRow(ctx, q, productID).Scan(&minPrice)
	if err != nil {
		return false, fmt.Errorf("db.IsLowestIn30Days: %w", err)
	}
	return price <= minPrice, nil
}

// ─────────────────────────────────────────
// Alerts
// ─────────────────────────────────────────

// InsertAlert creates a price alert for a product.
func InsertAlert(ctx context.Context, productID uuid.UUID, targetPrice float64) (*Alert, error) {
	const q = `
		INSERT INTO alerts (product_id, target_price)
		VALUES ($1, $2)
		RETURNING id, product_id, target_price, triggered, created_at, triggered_at
	`
	var a Alert
	err := Pool.QueryRow(ctx, q, productID, targetPrice).
		Scan(&a.ID, &a.ProductID, &a.TargetPrice, &a.Triggered, &a.CreatedAt, &a.TriggeredAt)
	if err != nil {
		return nil, fmt.Errorf("db.InsertAlert: %w", err)
	}
	return &a, nil
}

// GetPendingAlerts returns all untriggered alerts for a product.
func GetPendingAlerts(ctx context.Context, productID uuid.UUID) ([]Alert, error) {
	const q = `
		SELECT id, product_id, target_price, triggered, created_at, triggered_at
		FROM alerts
		WHERE product_id = $1 AND triggered = FALSE
	`
	rows, err := Pool.Query(ctx, q, productID)
	if err != nil {
		return nil, fmt.Errorf("db.GetPendingAlerts: %w", err)
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.ProductID, &a.TargetPrice, &a.Triggered, &a.CreatedAt, &a.TriggeredAt); err != nil {
			return nil, fmt.Errorf("db.GetPendingAlerts scan: %w", err)
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// TriggerAlert marks an alert as triggered with the current timestamp.
func TriggerAlert(ctx context.Context, alertID uuid.UUID) error {
	const q = `
		UPDATE alerts
		SET triggered = TRUE, triggered_at = NOW()
		WHERE id = $1
	`
	ct, err := Pool.Exec(ctx, q, alertID)
	if err != nil {
		return fmt.Errorf("db.TriggerAlert: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("db.TriggerAlert: alert %s not found", alertID)
	}
	return nil
}

// GetAllProductsForScraping returns all product IDs and URLs for the scheduler.
func GetAllProductsForScraping(ctx context.Context) ([]Product, error) {
	const q = `
		SELECT id, url, name, platform, created_at
		FROM products
		ORDER BY created_at ASC
	`
	rows, err := Pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("db.GetAllProductsForScraping: %w", err)
	}
	defer rows.Close()

	var products []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.URL, &p.Name, &p.Platform, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("db.GetAllProductsForScraping scan: %w", err)
		}
		products = append(products, p)
	}
	return products, rows.Err()
}

func InitDB(ctx context.Context) {
	_, err := Pool.Exec(ctx, `
		ALTER TABLE products 
		ADD COLUMN IF NOT EXISTS price_source VARCHAR(20) DEFAULT 'simulated'
	`)
	if err != nil {
		log.Printf("[DB] Migration warning: %v", err)
		// Don't fatal — column may already exist
	}
}
