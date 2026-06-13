package simulator

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"
)

// StartTicker runs the price simulator in a background loop.
// It generates new prices for all products at the configured interval
// and publishes them to Kafka.
func (s *Simulator) StartTicker(ctx context.Context) {
	intervalMinutes := 30
	if v := os.Getenv("SCRAPE_INTERVAL_MINUTES"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			intervalMinutes = parsed
		}
	}

	ticker := time.NewTicker(time.Duration(intervalMinutes) * time.Minute)
	defer ticker.Stop()

	log.Printf("[simulator] 🔄 ticker started — generating prices every %d minutes", intervalMinutes)

	// Run one immediate tick on startup
	s.Tick(ctx, time.Now())

	for {
		select {
		case <-ctx.Done():
			log.Println("[simulator] ticker stopped — context cancelled")
			return
		case t := <-ticker.C:
			log.Printf("[simulator] ⏱ tick at %s", t.Format(time.RFC3339))
			s.Tick(ctx, t)
		}
	}
}
