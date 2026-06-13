package kafka

import "time"

// ─────────────────────────────────────────
// Message types — every Kafka message is JSON encoded
// ─────────────────────────────────────────

// ScrapeRequestedMsg is published to pricespy.scrape.requested
// when a user adds a product or the scheduler triggers a refresh.
type ScrapeRequestedMsg struct {
	ProductID string `json:"product_id"`
	URL       string `json:"url"`
	Platform  string `json:"platform"`
	RequestAt string `json:"requested_at"` // RFC3339
}

// PriceScrapedMsg is published to pricespy.price.scraped
// by the scraper after fetching the price.
type PriceScrapedMsg struct {
	ProductID string  `json:"product_id"`
	URL       string  `json:"url"`
	Platform  string  `json:"platform"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	InStock   bool    `json:"in_stock"`
	ScrapedAt string  `json:"scraped_at"` // RFC3339
	Error     string  `json:"error,omitempty"`
}

// PriceStoredMsg is published to pricespy.price.stored
// after the consumer successfully writes the price to Postgres.
type PriceStoredMsg struct {
	PriceHistoryID string  `json:"price_history_id"`
	ProductID      string  `json:"product_id"`
	Price          float64 `json:"price"`
	InStock        bool    `json:"in_stock"`
	StoredAt       string  `json:"stored_at"` // RFC3339
}

// TimeNow returns the current UTC time formatted as RFC3339.
func TimeNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}
