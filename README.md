# PriceSpy

> A Kafka-powered price tracking platform that monitors Amazon and Flipkart products, maintains historical price history, and generates real-time buy/wait recommendations through an event-driven architecture.

![Go](https://img.shields.io/badge/Go-1.22-blue)
![React](https://img.shields.io/badge/React-18-blue)
![Kafka](https://img.shields.io/badge/Apache-Kafka-black)
![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-blue)
![Docker](https://img.shields.io/badge/Docker-Containerized-blue)

---

## Why I Built This

Most price trackers are simple CRUD applications with a scheduled scraper.

I wanted to build something closer to a real data platform:

- Event-driven architecture
- Kafka topics and consumers
- Replayable price history
- Time-series analytics
- Failure-tolerant ingestion
- Real-world scraping constraints

PriceSpy became an experiment in building a miniature data pipeline similar to the systems used by modern analytics companies.

---

# Screenshots

## Dashboard

![Dashboard](docs/screenshots/dashboard.png)

Real tracked products displayed in a unified data grid with buy/wait recommendations.

---

## Historical Price Analysis

![Chart](docs/screenshots/chart.png)

Interactive 7D / 30D / All-time price history showing:

- Current price
- 30-day high
- 30-day low
- Average price
- Historical trends

---

## Adding Products

![Add Product](docs/screenshots/add-product.png)

Track products directly from Amazon and Flipkart URLs.

---

## Live Simulation Events

![Flash Sale](docs/screenshots/flash-sale.png)

Demo controls generate:

- Flash sales
- Out-of-stock events
- Competitor price drops
- Fast-forwarded market activity

Every event flows through Kafka before reaching PostgreSQL.

---

## Kafka Event Processing

![Kafka Logs](docs/screenshots/kafka-logs.png)

Kafka consumer processing price events and persisting them to PostgreSQL.

---

# Architecture

```text
                    User
                      │
                      ▼
              React Frontend
                      │
                      ▼
               Gin REST API
                      │
                      ▼
              Kafka Producer
                      │
                      ▼

        ┌──────────────────────────┐
        │       Apache Kafka       │
        ├──────────────────────────┤
        │ scrape.requested         │
        │ price.scraped            │
        │ price.stored             │
        └──────────────────────────┘

                      │
                      ▼

               Kafka Consumer
                      │
                      ▼

                 PostgreSQL
                      │
                      ▼

             Analytics + Charts
```

---

# System Design

```text
                   ┌─────────────┐
                   │ Product URL │
                   └──────┬──────┘
                          │
                          ▼

                Metadata Extraction

          JSON-LD
              │
              ▼

         OpenGraph
              │
              ▼

       URL Estimation

              │
              ▼

         Product Created
              │
              ▼

           Kafka Event
              │
              ▼

          Consumer Group
              │
              ▼

          PostgreSQL
              │
              ▼

           Dashboard
```

---

# Tech Stack

| Layer | Technology |
|---------|------------|
| Backend | Go + Gin |
| Messaging | Apache Kafka |
| Database | PostgreSQL |
| Frontend | React + Vite |
| Charts | Recharts |
| Metrics | Prometheus |
| RPC | gRPC |
| Containers | Docker Compose |

---

# Key Engineering Decisions

## 1. Single Writer Pattern

Only Kafka consumers are allowed to write to PostgreSQL.

```text
Producer → Kafka → Consumer → Database
```

Benefits:

- No concurrent write conflicts
- Replayable event history
- Easier scaling
- Strong audit trail

---

## 2. Multi-Layer Product Extraction

Amazon and Flipkart aggressively limit scraping.

Instead of relying on fragile CSS selectors:

### Layer 1 — JSON-LD

```json
{
  "@type": "Product",
  "offers": {
    "price": "79999"
  }
}
```

### Layer 2 — OpenGraph

```html
<meta property="product:price:amount">
```

### Layer 3 — URL Estimation

```text
iphone-15
   ↓
₹75k - ₹85k
```

This guarantees graceful degradation instead of complete failure.

---

## 3. Mean-Reverting Price Simulator

Naive random walks produced unrealistic prices.

Instead:

```text
new_price =
current_price
+ random_noise
+ 0.1 × (base_price - current_price)
```

The simulator also models:

- Weekend discounts
- Midnight sales
- Competitor reactions
- Stock events

The result is synthetic price history that closely resembles real e-commerce price behavior.

---

## 4. Historical Backfill Optimization

Original implementation:

```text
30 Days History
        ↓
      Kafka
        ↓
    Consumer
        ↓
   PostgreSQL
```

Problem:

- 7000+ startup events
- ~90 second startup time

Solution:

```text
Historical Backfill → PostgreSQL

Live Updates → Kafka
```

Startup time dropped from:

```text
~90 seconds → <3 seconds
```

---

# Kafka Topic Design

## pricespy.scrape.requested

Published by:

- Scheduler
- Product creation

Payload:

```json
{
  "product_id": "...",
  "url": "...",
  "timestamp": "..."
}
```

---

## pricespy.price.scraped

Published by:

- Simulator

Payload:

```json
{
  "product_id": "...",
  "price": 53257,
  "currency": "INR",
  "in_stock": true
}
```

---

## pricespy.price.stored

Published by:

- Consumer after successful DB write

Purpose:

Future alert-service integration.

---

# Observability

Prometheus metrics:

```text
pricespy_products_tracked_total

pricespy_scrapes_total

pricespy_alerts_triggered_total

pricespy_kafka_messages_processed_total
```

---

# Performance Improvements

| Issue | Impact | Fix |
|---------|---------|---------|
| Kafka flooding during startup | 90s startup time | Direct DB backfill |
| Duplicate products | Ghost rows | UNIQUE(url) |
| Scraper failures | Missing prices | Multi-layer extraction |
| Random walk drift | Unrealistic charts | Mean reversion |
| Dashboard clutter | Poor UX | Linear-inspired redesign |

---

# Project Structure

```text
pricespy/

├── backend
│   ├── api
│   ├── kafka
│   ├── scraper
│   ├── simulator
│   ├── grpc
│   ├── metrics
│   └── db

├── frontend
│   ├── components
│   ├── pages
│   └── services

├── docs
│   └── screenshots

├── docker-compose.yml
└── README.md
```

---

# What I Learned

Building PriceSpy taught me significantly more about distributed systems than about price tracking.

The challenging parts were not UI-related.

They were:

- Designing Kafka event flows
- Making services idempotent
- Handling scraper failures gracefully
- Maintaining replayable history
- Preventing event storms
- Building realistic simulations

Most tutorials stop at "store data in a database."

The harder part is designing systems that continue working when components fail.

---

# Future Improvements

### Alert Service

```text
price.stored
      ↓
 Alert Service
      ↓
 Email / SMS
```

### Consumer Scaling

```text
Consumer Group

├─ Consumer 1
├─ Consumer 2
└─ Consumer 3
```

### Browser-Based Scraping

Using Playwright for:

- JavaScript-rendered pages
- Better anti-bot resilience
- Improved accuracy

### Price Forecasting

Using historical data to generate:

- Price predictions
- Trend confidence scores
- Expected savings estimates

---

# Running Locally

```bash
git clone https://github.com/Lakshay-Pareek/pricespy

docker compose up -d

cd backend
go run main.go

cd frontend
npm install
npm run dev
```

---

# Why This Project Matters

PriceSpy isn't a scraper.

It's a small-scale event-driven data platform.

The goal was to explore the same architectural ideas used in production analytics systems:

- Kafka as a source of truth
- Event-driven processing
- Time-series storage
- Consumer isolation
- Replayable pipelines
- Observability

That's what made the project worth building.

---

**Lakshay Pareek**  
NIT Silchar  
Backend Engineering • Distributed Systems • Data Infrastructure
