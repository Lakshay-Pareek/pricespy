package kafka

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// Topic names — single source of truth
const (
	TopicScrapeRequested = "pricespy.scrape.requested"
	TopicPriceScraped    = "pricespy.price.scraped"
	TopicPriceStored     = "pricespy.price.stored"
)

// Producer wraps a map of kafka-go writers, one per topic.
type Producer struct {
	writers map[string]*kafkago.Writer
}

// NewProducer creates a Producer connected to the brokers in KAFKA_BROKERS env var.
// Brokers are comma-separated, e.g. "localhost:9092,localhost:9093".
func NewProducer() *Producer {
	brokers := brokerList()

	writers := make(map[string]*kafkago.Writer, 3)
	for _, topic := range []string{TopicScrapeRequested, TopicPriceScraped, TopicPriceStored} {
		w := &kafkago.Writer{
			Addr:         kafkago.TCP(brokers...),
			Topic:        topic,
			Balancer:     &kafkago.LeastBytes{},
			RequiredAcks: kafkago.RequireOne,
			MaxAttempts:  5,
			// Batch settings for throughput
			BatchSize:    100,
			BatchTimeout: 10 * time.Millisecond,
			// Async=false so we know the message landed before continuing
			Async: false,
		}
		writers[topic] = w
		log.Printf("[kafka] producer ready for topic: %s (brokers: %s)", topic, strings.Join(brokers, ","))
	}

	return &Producer{writers: writers}
}

// PublishScrapeRequested sends a scrape job request to Kafka.
func (p *Producer) PublishScrapeRequested(ctx context.Context, msg ScrapeRequestedMsg) error {
	return p.publish(ctx, TopicScrapeRequested, msg.ProductID, msg)
}

// PublishPriceScraped sends the raw scrape result to Kafka.
func (p *Producer) PublishPriceScraped(ctx context.Context, msg PriceScrapedMsg) error {
	return p.publish(ctx, TopicPriceScraped, msg.ProductID, msg)
}

// PublishPriceStored sends a confirmation after the price is written to Postgres.
func (p *Producer) PublishPriceStored(ctx context.Context, msg PriceStoredMsg) error {
	return p.publish(ctx, TopicPriceStored, msg.ProductID, msg)
}

// publish is the internal helper — JSON-encodes the payload and writes it.
// The product_id is used as the Kafka message key for partition affinity
// (all events for the same product go to the same partition).
func (p *Producer) publish(ctx context.Context, topic, key string, payload any) error {
	w, ok := p.writers[topic]
	if !ok {
		return fmt.Errorf("kafka: no writer for topic %q", topic)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("kafka: marshal payload for topic %q: %w", topic, err)
	}

	msg := kafkago.Message{
		Key:   []byte(key),
		Value: data,
	}

	if err := w.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("kafka: write to topic %q: %w", topic, err)
	}

	log.Printf("[kafka] published to %s | key=%s | %d bytes", topic, key, len(data))
	return nil
}

// Close shuts down all writers gracefully.
func (p *Producer) Close() {
	for topic, w := range p.writers {
		if err := w.Close(); err != nil {
			log.Printf("[kafka] error closing producer for topic %s: %v", topic, err)
		} else {
			log.Printf("[kafka] producer closed: %s", topic)
		}
	}
}

// ─────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────

// brokerList reads KAFKA_BROKERS env var and splits by comma.
func brokerList() []string {
	raw := os.Getenv("KAFKA_BROKERS")
	if raw == "" {
		raw = "localhost:9092"
	}
	parts := strings.Split(raw, ",")
	var brokers []string
	for _, b := range parts {
		b = strings.TrimSpace(b)
		if b != "" {
			brokers = append(brokers, b)
		}
	}
	return brokers
}
