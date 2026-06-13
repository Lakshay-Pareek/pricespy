package kafka

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/google/uuid"
	"github.com/pricespy/backend/db"
)

// Consumer listens to pricespy.price.scraped, persists prices to Postgres,
// then publishes a confirmation to pricespy.price.stored.
type Consumer struct {
	reader   *kafkago.Reader
	producer *Producer
}

// NewConsumer creates a Consumer for the pricespy.price.scraped topic.
func NewConsumer(producer *Producer) *Consumer {
	groupID := os.Getenv("KAFKA_GROUP_ID")
	if groupID == "" {
		groupID = "pricespy-consumer-group"
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:        brokerList(),
		GroupID:        groupID,
		Topic:          TopicPriceScraped,
		MinBytes:       1,            // fetch as soon as 1 byte is available
		MaxBytes:       10 << 20,     // 10 MB max per fetch
		MaxWait:        1 * time.Second,
		CommitInterval: 1 * time.Second, // auto-commit offsets every second
		StartOffset:    kafkago.LastOffset,
		// Retry settings
		MaxAttempts: 10,
	})

	log.Printf("[kafka] consumer ready | topic=%s | group=%s | brokers=%v",
		TopicPriceScraped, groupID, brokerList())

	return &Consumer{
		reader:   reader,
		producer: producer,
	}
}

// Start begins consuming messages in a blocking loop.
// Pass a cancellable context to stop gracefully.
func (c *Consumer) Start(ctx context.Context) {
	log.Println("[kafka] consumer started — waiting for price scraped events...")

	for {
		// FetchMessage does NOT commit the offset — we do that manually after processing
		m, err := c.reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				log.Println("[kafka] consumer context cancelled — stopping")
				return
			}
			log.Printf("[kafka] fetch error: %v — retrying in 2s", err)
			time.Sleep(2 * time.Second)
			continue
		}

		log.Printf("[kafka] received message | topic=%s partition=%d offset=%d key=%s",
			m.Topic, m.Partition, m.Offset, string(m.Key))

		// Process — if it fails we log but still commit to avoid poison pill loops
		if err := c.handlePriceScraped(ctx, m.Value); err != nil {
			log.Printf("[kafka] handlePriceScraped error: %v", err)
		}

		// Commit the offset — message is acknowledged
		if err := c.reader.CommitMessages(ctx, m); err != nil {
			log.Printf("[kafka] commit error: %v", err)
		}
	}
}

// handlePriceScraped processes one PriceScrapedMsg:
//  1. Decode JSON
//  2. Write to price_history via pgx
//  3. Publish PriceStoredMsg to pricespy.price.stored
func (c *Consumer) handlePriceScraped(ctx context.Context, raw []byte) error {
	var msg PriceScrapedMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return fmt.Errorf("unmarshal PriceScrapedMsg: %w", err)
	}

	// If the scraper reported an error, skip DB write but log it
	if msg.Error != "" {
		log.Printf("[kafka] scrape error reported for product %s: %s", msg.ProductID, msg.Error)
		return nil
	}

	productID, err := uuid.Parse(msg.ProductID)
	if err != nil {
		return fmt.Errorf("parse product_id %q: %w", msg.ProductID, err)
	}

	// ── Write price to Postgres ────────────────────────────────────
	ph, err := db.InsertPriceHistory(ctx, productID, msg.Price, msg.Currency, msg.InStock)
	if err != nil {
		return fmt.Errorf("InsertPriceHistory for product %s: %w", msg.ProductID, err)
	}

	log.Printf("[kafka] stored price ₹%.2f for product %s (in_stock=%v)",
		ph.Price, ph.ProductID, ph.InStock)

	// ── Check pending alerts ───────────────────────────────────────
	go c.checkAlerts(ctx, productID, ph.Price)

	// ── Publish confirmation ───────────────────────────────────────
	storedMsg := PriceStoredMsg{
		PriceHistoryID: ph.ID.String(),
		ProductID:      ph.ProductID.String(),
		Price:          ph.Price,
		InStock:        ph.InStock,
		StoredAt:       ph.ScrapedAt.UTC().Format(time.RFC3339),
	}
	if err := c.producer.PublishPriceStored(ctx, storedMsg); err != nil {
		// Non-fatal — price is already stored in DB
		log.Printf("[kafka] warn: could not publish price.stored event: %v", err)
	}

	return nil
}

// checkAlerts fires any pending alerts whose target price has been reached.
func (c *Consumer) checkAlerts(ctx context.Context, productID uuid.UUID, currentPrice float64) {
	alerts, err := db.GetPendingAlerts(ctx, productID)
	if err != nil {
		log.Printf("[kafka] checkAlerts: %v", err)
		return
	}

	for _, a := range alerts {
		if currentPrice <= a.TargetPrice {
			log.Printf("[kafka] 🔔 alert triggered! product=%s target=₹%.2f current=₹%.2f",
				productID, a.TargetPrice, currentPrice)
			if err := db.TriggerAlert(ctx, a.ID); err != nil {
				log.Printf("[kafka] TriggerAlert %s: %v", a.ID, err)
			}
		}
	}
}

// Close shuts down the reader cleanly.
func (c *Consumer) Close() {
	if err := c.reader.Close(); err != nil {
		log.Printf("[kafka] error closing consumer reader: %v", err)
	} else {
		log.Println("[kafka] consumer reader closed")
	}
}
