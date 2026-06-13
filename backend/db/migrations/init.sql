-- =============================================================
-- PriceSpy — PostgreSQL Schema
-- Auto-executed by Docker on first container start
-- =============================================================

-- Extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";  -- for gen_random_uuid()

-- ─────────────────────────────────────────
-- ENUM: platform
-- ─────────────────────────────────────────
DO $$ BEGIN
  CREATE TYPE platform_type AS ENUM ('amazon', 'flipkart');
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

-- ─────────────────────────────────────────
-- TABLE: products
-- ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS products (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  url         TEXT        NOT NULL UNIQUE,
  name        TEXT        NOT NULL DEFAULT '',
  platform    platform_type NOT NULL,
  price_source VARCHAR(20) DEFAULT 'simulated',
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_products_platform ON products (platform);
CREATE INDEX IF NOT EXISTS idx_products_created_at ON products (created_at DESC);

-- ─────────────────────────────────────────
-- TABLE: price_history
-- ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS price_history (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id  UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  price       NUMERIC(12, 2) NOT NULL,
  currency    TEXT        NOT NULL DEFAULT 'INR',
  scraped_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  in_stock    BOOLEAN     NOT NULL DEFAULT TRUE
);

CREATE INDEX IF NOT EXISTS idx_price_history_product_id ON price_history (product_id);
CREATE INDEX IF NOT EXISTS idx_price_history_scraped_at ON price_history (scraped_at DESC);
-- Composite index for fast 7d / 30d window queries
CREATE INDEX IF NOT EXISTS idx_price_history_product_time
  ON price_history (product_id, scraped_at DESC);

-- ─────────────────────────────────────────
-- TABLE: alerts
-- ─────────────────────────────────────────
CREATE TABLE IF NOT EXISTS alerts (
  id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  product_id    UUID        NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  target_price  NUMERIC(12, 2) NOT NULL,
  triggered     BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  triggered_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_alerts_product_id ON alerts (product_id);
CREATE INDEX IF NOT EXISTS idx_alerts_triggered ON alerts (triggered) WHERE triggered = FALSE;

-- ─────────────────────────────────────────
-- Helpful views
-- ─────────────────────────────────────────

-- Latest price per product
CREATE OR REPLACE VIEW latest_prices AS
  SELECT DISTINCT ON (product_id)
    ph.product_id,
    ph.price,
    ph.currency,
    ph.scraped_at,
    ph.in_stock
  FROM price_history ph
  ORDER BY ph.product_id, ph.scraped_at DESC;

-- 7-day average per product
CREATE OR REPLACE VIEW avg_price_7d AS
  SELECT
    product_id,
    ROUND(AVG(price), 2) AS avg_price,
    MIN(price) AS min_price,
    MAX(price) AS max_price,
    COUNT(*) AS data_points
  FROM price_history
  WHERE scraped_at >= NOW() - INTERVAL '7 days'
  GROUP BY product_id;

-- 30-day average per product
CREATE OR REPLACE VIEW avg_price_30d AS
  SELECT
    product_id,
    ROUND(AVG(price), 2) AS avg_price,
    MIN(price) AS min_price,
    MAX(price) AS max_price,
    COUNT(*) AS data_points
  FROM price_history
  WHERE scraped_at >= NOW() - INTERVAL '30 days'
  GROUP BY product_id;
