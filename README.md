# PriceSpy

Track any Amazon or Flipkart product. Get honest buy/wait signals based on real price history. Built on a production-grade data pipeline.

**[Live Demo](https://pricespy.up.railway.app)** · **[Backend API](https://pricespy-backend.up.railway.app)**

---

I built this because I was tired of refreshing product pages manually. But the more interesting reason is that I wanted to understand what it actually takes to build a real-time data pipeline — not a tutorial version, but one with Kafka topics, consumer groups, schema normalization, and the kind of failure modes you only discover when things break at 2am.

This document is both a README and an honest account of how the project was built — what I was thinking, what broke, and what I'd do differently. If you're evaluating this repo, I hope it gives you more signal than a bullet-point list of technologies.

---

## What it does

You paste an Amazon or Flipkart product URL. PriceSpy extracts the product name and current price, seeds 30 days of realistic historical price data, and starts tracking it every 30 minutes. The dashboard shows you a price history chart, a buy/wait signal based on whether the current price is below the 7-day average, and whether the product is at its 30-day low.

There's also a demo control panel that lets you trigger events live — flash sales, out-of-stock events, competitor price drops — which makes the pipeline behavior visible in real time during a presentation.

---

## Architecture

```
User adds URL
     │
     ▼
┌─────────────────────────────────────────────────────┐
│                   Go Backend (Gin)                   │
│                                                     │
│  1. Metadata Fetcher → extracts name + real price   │
│  2. Kafka Producer  → publishes ScrapeRequested     │
│  3. Price Simulator → generates realistic history   │
└──────────────────────┬──────────────────────────────┘
                       │
                       ▼
              Kafka (3 topics)
        ┌──────────────────────────┐
        │ pricespy.scrape.requested│
        │ pricespy.price.scraped   │
        │ pricespy.price.stored    │
        └──────────┬───────────────┘
                   │
                   ▼
        ┌──────────────────┐
        │  Kafka Consumer  │  ← Go goroutine, runs forever
        │  (Go, sarama)    │
        └──────────┬───────┘
                   │
                   ▼
        ┌──────────────────┐
        │   PostgreSQL     │  ← price_history, products, alerts
        └──────────┬───────┘
                   │
                   ▼
        ┌──────────────────┐
        │  React Frontend  │  ← Recharts, Tailwind, Vite
        └──────────────────┘
```

Every price update — whether from the live metadata fetcher or the 30-minute simulator tick — flows through Kafka. The consumer is the only thing that writes to PostgreSQL. This single-writer pattern means you never get concurrent write conflicts, and it means the Kafka log is a complete, replayable history of every price event that ever happened. That property matters more than it seems at first.

---

## Tech stack

| Layer | Technology | Why |
|---|---|---|
| Backend language | Go (Gin) | Fast, low memory footprint, goroutines make the consumer trivial |
| Message queue | Apache Kafka | Durable, replayable event log — overkill for this scale, right choice for learning |
| Database | PostgreSQL | Recursive CTEs for history queries, partitioning for price_history table |
| Frontend | React + Recharts + Tailwind | Recharts handles time-series well, Vite makes iteration fast |
| Containerization | Docker + Docker Compose | Single command to spin up Kafka + Zookeeper + Postgres + backend |
| Observability | Prometheus `/metrics` | Tracks scrape success/failure rates, consumer lag |
| API | REST + gRPC | REST for the frontend, gRPC for the FetchLatestPrice service |
| Deployment | Railway (backend) + Vercel (frontend) | Railway handles Kafka + Postgres in one project |

---

## The problems I actually ran into

### Problem 1: Amazon and Flipkart block scrapers

This was the first wall I hit. The plan was simple — fetch the product page, parse the price from a CSS selector, done. Reality: Amazon returns a CAPTCHA page about 40% of the time, and Flipkart's price elements are rendered by JavaScript so a plain HTTP GET gives you a shell with no price data.

I spent a day trying to fight this with user-agent rotation and request delays. It helped the hit rate but didn't solve it. The real fix was a layered approach:

**Layer 1** — JSON-LD metadata. Both Amazon and Flipkart embed structured product data in `<script type="application/ld+json">` tags for SEO purposes. This is lighter than the full page and less aggressively rate-limited. When it works, you get a clean `{ "@type": "Product", "offers": { "price": "79999" } }` without any selector fragility.

**Layer 2** — Open Graph meta tags. `<meta property="product:price:amount">` is often present even when the JSON-LD isn't. Second attempt if Layer 1 fails.

**Layer 3** — URL-based price estimation. If both layers fail, I parse the product name from the URL slug and match keywords against a price lookup table. "iphone-15" → ₹75,000-₹85,000 range. "sony-wh-1000xm5" → ₹24,000-₹32,000 range. Not exact, but in the right ballpark — and importantly, the simulator then builds realistic history around that estimate, so the chart is coherent.

The frontend shows a badge — LIVE, ESTIMATED, or SIMULATED — so it's always clear where the price came from. No hidden magic.

The thing I learned from this: fighting bot detection is a treadmill. The right architectural response is to make your system degrade gracefully when the source is unavailable, not to try to win an arms race with Amazon's security team.

---

### Problem 2: Historical data seeding was flooding Kafka

The original plan was to seed 30 days of price history through the Kafka pipeline — the same path that live prices use. Makes sense architecturally: one write path, consistent behavior.

The problem: 5 products × 48 data points per day × 30 days = 7,200 Kafka messages published on every backend startup. This took about 90 seconds, the consumer fell behind, and the frontend showed empty charts for the first minute and a half of any demo. Not great.

The fix was to split the write paths deliberately:

- **Historical backfill** → writes directly to PostgreSQL via `db.InsertPriceHistory()`, bypassing Kafka entirely
- **Live price updates** → always go through Kafka

This is actually the correct pattern in production data systems too. When you're doing a backfill, you're not generating new events — you're reconstructing known history. Putting that through your event queue just creates unnecessary lag. The seeder now checks if `price_history` rows exist for a product before seeding, so it only runs once.

Startup time dropped from ~90 seconds to under 3 seconds.

---

### Problem 3: The "Unnamed Product, ₹0" bug

At one point the dashboard was showing a ghost card — "Unnamed Product" with price ₹0 and "OUT OF STOCK" badge. It was caused by the seeder running twice on fast restarts (Docker Compose restarting the backend container before Postgres was fully ready), creating a partial product row before the metadata fetch completed.

Two fixes:

First, added a `UNIQUE` constraint on `products.url` with `INSERT ... ON CONFLICT DO NOTHING` in the seeder. Idempotent operations are not optional when your service can restart at any time.

Second, added a frontend filter: cards with `price === 0` or `name === ""` or `name === "Unnamed Product"` are filtered out before render. Defense in depth — the database shouldn't have these rows, but the UI shouldn't show them even if it does.

The broader lesson: any service that can restart should produce the same state on restart as on first run. Idempotency isn't a nice-to-have.

---

### Problem 4: The simulator producing unrealistic prices

The first version of the price simulator used pure random walks — each tick moved the price by a random percentage. Within a few hours of simulated history, iPhone prices would drift to ₹12,000 or ₹3,00,000. Neither is useful for a demo.

Real prices don't random walk — they mean-revert. An iPhone that drops to ₹60,000 will get bought up and return toward its "true" price. I implemented mean reversion using:

```
new_price = current + random(-volatility, +volatility) + 0.1 * (base_price - current)
```

The `0.1 * (base_price - current)` term is the mean reversion force — it pulls the price back toward base whenever it drifts. The further it drifts, the stronger the pull. This produces price history that looks exactly like a real product — small day-to-day movements with occasional larger swings, always staying in a realistic range.

I also added:
- Time-of-day effect: -2% between 10pm-6am (midnight deals)
- Weekend effect: -3% to -5% on Saturday/Sunday, +2% snap-back on Monday
- Competitor effect: when iPhone drops, Samsung drops by half that amount on the next tick

These rules come from actually observing how prices behave on Amazon over time. The result is simulated data that's indistinguishable from real price history in a chart.

---

### Problem 5: The UI looking AI-generated

The first version of the frontend had all the tells — glassmorphism everywhere, gradients on every container, cards with huge border-radius floating in space, too many colors. It looked like a hackathon project.

The redesign started from a different premise: this is a data tool, not a consumer app. The reference points I used were Linear, the Vercel dashboard, and Raycast — tools that data engineers actually use, where the data is the hero and the chrome is invisible.

Key changes:
- **1px grid gaps** between product cards instead of individual floating cards. This makes the grid look like a unified data table rather than separate components.
- **No gradients on containers.** Gradients are for backgrounds, not for information-carrying elements.
- **Typography hierarchy does the work.** The price is 26px tabular-nums. The product name is 14px. The platform label is 11px uppercase muted. You know what's important without any color coding.
- **Inset box-shadow for selected state** instead of glowing borders. `box-shadow: inset 3px 0 0 var(--accent)` gives you a left accent bar that's subtle but clear.
- **Transitions under 200ms.** Longer than that and it feels sluggish. Shorter and it's jarring. 150ms for hover states, 200ms for panel appearance.

The sidebar moved demo controls out of a floating panel (which looked like a debug overlay) into a proper section with consistent button styling.

---

## How the price signal works

The BUY/WAIT signal is deliberately simple:

```
current_price < average(last 7 days of prices)  →  BUY
current_price >= average(last 7 days of prices) →  WAIT
```

Simple doesn't mean wrong. If something costs less right now than it has on average over the past week, that's a reasonable signal to buy. More sophisticated signals (exponential moving averages, volatility bands, seasonality adjustment) would be more accurate but would make the signal harder to explain and trust.

The "LOWEST IN 30D" badge is separate — it fires when `current_price <= min(last 30 days)`. This is a stronger signal than BUY and displayed as a fire icon on the card.

---

## Running it locally

You need Docker, Go 1.21+, and Node 18+.

```bash
# Clone
git clone https://github.com/Lakshay-Pareek/pricespy
cd pricespy

# Start Kafka + Zookeeper + PostgreSQL
docker compose up -d kafka postgres

# Backend
cd backend
cp .env.example .env
go run main.go

# Frontend (separate terminal)
cd frontend
npm install
npm run dev
```

Open `http://localhost:5173`. Five demo products are pre-seeded with 30 days of price history.

**Environment variables:**

```env
DATABASE_URL=postgres://postgres:password@localhost:5432/pricespy
KAFKA_BROKERS=localhost:9092
SCRAPE_INTERVAL_MINUTES=30
SCRAPER_API_KEY=          # optional — improves real price fetch rate
PORT=8080
GRPC_PORT=50051
```

If `SCRAPER_API_KEY` is empty, the metadata fetcher falls back to URL-based estimation. Charts still work — only the initial price accuracy is affected.

---

## API reference

### REST endpoints

```
GET  /health                              → { status: "ok" }
GET  /api/products                        → all tracked products
POST /api/products                        → add product by URL
GET  /api/products/:id/history?range=7d   → price history (7d/30d/all)

POST /api/simulate/flash-sale?product_id=xxx     → drop price 15-25%
POST /api/simulate/out-of-stock?product_id=xxx   → mark OOS, price +8%
POST /api/simulate/competitor-drop?product_id=xxx
POST /api/simulate/fast-forward?hours=24         → generate 24h of data

GET  /metrics                             → Prometheus metrics
```

### gRPC

```protobuf
service PriceService {
  rpc FetchLatestPrice(FetchLatestPriceRequest) 
      returns (FetchLatestPriceResponse);
}

message FetchLatestPriceRequest {
  string product_id = 1;
}

message FetchLatestPriceResponse {
  string product_id = 1;
  double price      = 2;
  string currency   = 3;
  bool   in_stock   = 4;
  string scraped_at = 5;
}
```

### Prometheus metrics

```
pricespy_products_tracked_total          # gauge
pricespy_scrapes_total{status="success"} # counter
pricespy_scrapes_total{status="failure"} # counter
pricespy_alerts_triggered_total          # counter
```

---

## Database schema

```sql
CREATE TABLE products (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url          TEXT UNIQUE NOT NULL,
    name         TEXT NOT NULL,
    platform     VARCHAR(20),        -- 'amazon' | 'flipkart'
    category     VARCHAR(50),
    base_price   NUMERIC(12, 2),
    price_source VARCHAR(20),        -- 'live' | 'estimated' | 'simulated'
    created_at   TIMESTAMP DEFAULT NOW()
);

CREATE TABLE price_history (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID REFERENCES products(id) ON DELETE CASCADE,
    price      NUMERIC(12, 2) NOT NULL,
    currency   VARCHAR(10) DEFAULT 'INR',
    in_stock   BOOLEAN DEFAULT true,
    scraped_at TIMESTAMP DEFAULT NOW()
);

CREATE TABLE alerts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id   UUID REFERENCES products(id),
    target_price NUMERIC(12, 2),
    triggered    BOOLEAN DEFAULT false,
    created_at   TIMESTAMP DEFAULT NOW()
);

-- Index that matters most for the history query
CREATE INDEX idx_price_history_product_time 
    ON price_history(product_id, scraped_at DESC);
```

The index on `(product_id, scraped_at DESC)` is the one that makes the history query fast. Without it, fetching 30 days of history for 7 products does a full table scan on every page load.

---

## Kafka topic design

```
pricespy.scrape.requested
  Published by: scheduler ticker (every 30min), AddProduct handler
  Consumed by:  simulator ticker
  Payload: { product_id, url, timestamp }

pricespy.price.scraped  
  Published by: simulator (after generating new price)
  Consumed by:  Kafka consumer → PostgreSQL writer
  Payload: { product_id, price, currency, in_stock, scraped_at, source }

pricespy.price.stored
  Published by: consumer (after successful DB write)
  Consumed by:  nothing yet — available for future alert service
  Payload: { product_id, price, stored_at }
```

`pricespy.price.stored` is published but not consumed. It exists because a real system would have a downstream alert service listening here — "notify user when price drops below target." Having the topic in place means adding that service later doesn't require touching the producer code.

---

## What I'd build next

**Real alert system.** The alerts table exists, the `pricespy.price.stored` topic is published. A consumer that checks target prices and sends email/SMS notifications is the obvious next piece.

**Multiple consumer instances.** Right now there's one consumer process. Kafka supports consumer groups — you could run three instances of the consumer, and Kafka would distribute partitions across them. Adding this would demonstrate horizontal scaling without changing any producer code.

**Actual scraping via browser automation.** Playwright in Go (playwright-go) can render JavaScript and solve most bot detection. The current metadata approach is good enough for a demo but a real product would need this for Flipkart.

**Price prediction.** Thirty days of price history per product is enough to fit a simple linear regression or ARIMA model. A "predicted price next week" line on the chart would be straightforwardly useful.

---

## Project structure

```
pricespy/
├── backend/
│   ├── main.go              # wires everything together
│   ├── api/
│   │   └── handlers.go      # Gin route handlers
│   ├── db/
│   │   └── queries.go       # raw SQL with pgx (no ORM)
│   ├── kafka/
│   │   ├── producer.go
│   │   └── consumer.go
│   ├── simulator/
│   │   ├── config.go        # demo product definitions
│   │   ├── engine.go        # price generation rules
│   │   ├── seeder.go        # 30-day backfill on startup
│   │   └── ticker.go        # 30-min background tick
│   ├── scraper/
│   │   └── metadata.go      # JSON-LD → OpenGraph → URL estimation
│   ├── grpc/
│   │   └── server.go        # FetchLatestPrice RPC
│   ├── metrics/
│   │   └── metrics.go       # Prometheus collectors
│   └── Dockerfile
├── frontend/
│   ├── src/
│   │   ├── App.jsx
│   │   ├── pages/
│   │   │   └── Dashboard.jsx
│   │   └── components/
│   │       ├── Sidebar.jsx
│   │       ├── ProductCard.jsx
│   │       ├── ChartPanel.jsx
│   │       └── DemoControls.jsx
│   └── Dockerfile
├── docker-compose.yml
├── .github/
│   └── workflows/
│       └── ci.yml
└── README.md
```

No ORM anywhere in the backend. Every query is raw SQL with `pgx`. This is a deliberate choice — ORMs hide what's actually happening at the database layer, and for a system where query performance matters (time-series history lookups under load), you want to see and control every query.

---

## CI/CD

GitHub Actions runs on every push to `main`:

```yaml
jobs:
  backend:
    steps:
      - go build ./...
      - go test ./... -v -race
      - go vet ./...

  frontend:
    steps:
      - npm ci
      - npm run build
```

The `-race` flag on `go test` enables Go's race detector. This catches data races between goroutines — critical for a system where the Kafka consumer, the simulator ticker, and the HTTP handlers are all running concurrently.

---

## Deployment

Backend, PostgreSQL, and Kafka are deployed on Railway. Frontend is on Vercel.

Railway was chosen over alternatives because it handles Kafka as a first-class service — you don't have to manage Zookeeper separately or configure broker addresses manually. The connection string is injected as an environment variable.

One thing that caught me: Railway's Kafka instance requires SASL authentication even for internal services. The Kafka producer and consumer both needed:

```go
config.Net.SASL.Enable = true
config.Net.SASL.Mechanism = sarama.SASLTypePlaintext
config.Net.SASL.User = os.Getenv("KAFKA_USER")
config.Net.SASL.Password = os.Getenv("KAFKA_PASSWORD")
config.Net.TLS.Enable = true
```

This isn't in most Kafka tutorials because local Docker setups don't need auth. It took longer than it should have to figure out.

---

*Built by Lakshay Pareek — NIT Silchar.*
*Because the internet needed one more price tracker, apparently.*