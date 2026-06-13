package kafka_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joho/godotenv"
	appkafka "github.com/pricespy/backend/kafka"
)

// TestProducer publishes one message to each topic.
// Run with: go test ./kafka/... -v -run TestProducer
// Requires Docker Compose Kafka to be running on localhost:9092.
func TestProducer(t *testing.T) {
	if os.Getenv("KAFKA_BROKERS") == "" {
		_ = godotenv.Load("../.env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	producer := appkafka.NewProducer()
	defer producer.Close()

	// ── Publish ScrapeRequested ────────────────────────────────────
	err := producer.PublishScrapeRequested(ctx, appkafka.ScrapeRequestedMsg{
		ProductID:  "test-product-id-001",
		URL:        "https://www.amazon.in/dp/TEST123",
		Platform:   "amazon",
		RequestAt:  appkafka.TimeNow(),
	})
	if err != nil {
		t.Fatalf("PublishScrapeRequested: %v", err)
	}
	t.Log("✓ Published to pricespy.scrape.requested")

	// ── Publish PriceScraped ───────────────────────────────────────
	err = producer.PublishPriceScraped(ctx, appkafka.PriceScrapedMsg{
		ProductID:  "test-product-id-001",
		URL:        "https://www.amazon.in/dp/TEST123",
		Platform:   "amazon",
		Price:      1499.00,
		Currency:   "INR",
		InStock:    true,
		ScrapedAt:  appkafka.TimeNow(),
	})
	if err != nil {
		t.Fatalf("PublishPriceScraped: %v", err)
	}
	t.Log("✓ Published to pricespy.price.scraped")

	// ── Publish PriceStored ────────────────────────────────────────
	err = producer.PublishPriceStored(ctx, appkafka.PriceStoredMsg{
		PriceHistoryID: "test-history-id-001",
		ProductID:      "test-product-id-001",
		Price:          1499.00,
		InStock:        true,
		StoredAt:       appkafka.TimeNow(),
	})
	if err != nil {
		t.Fatalf("PublishPriceStored: %v", err)
	}
	t.Log("✓ Published to pricespy.price.stored")
}
