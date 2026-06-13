package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/net/html"

	"github.com/pricespy/backend/db"
	appkafka "github.com/pricespy/backend/kafka"
	"github.com/pricespy/backend/scraper"
	"github.com/pricespy/backend/simulator"
)

// ─────────────────────────────────────────
// Handler dependencies (injected via NewRouter)
// ─────────────────────────────────────────

type Handler struct {
	producer  *appkafka.Producer
	simulator *simulator.Simulator
}

// ─────────────────────────────────────────
// Response shapes
// ─────────────────────────────────────────

type errorResponse struct {
	Error string `json:"error"`
}

type productResponse struct {
	db.Product
	CurrentPrice float64    `json:"current_price"`
	Currency     string     `json:"currency"`
	InStock      bool       `json:"in_stock"`
	PriceChange  float64    `json:"price_change_pct"` // % change vs 7d average
	Signal       string     `json:"signal"`           // "BUY" | "WAIT" | "INSUFFICIENT_DATA"
	IsLowest30d  bool       `json:"is_lowest_30d"`
	LastScraped  *time.Time `json:"last_scraped"`
	RecentPrices []float64  `json:"recent_prices"`
}

type addProductRequest struct {
	URL string `json:"url" binding:"required"`
}

type addProductResponse struct {
	Product      db.Product `json:"product"`
	PriceSource  string     `json:"price_source"`
	FetchedPrice float64    `json:"fetched_price"`
	Message      string     `json:"message"`
}

// ─────────────────────────────────────────
// Router
// ─────────────────────────────────────────

// NewRouter creates and returns a configured *gin.Engine.
func NewRouter(producer *appkafka.Producer, sim *simulator.Simulator) *gin.Engine {
	if os.Getenv("GIN_MODE") == "" {
		gin.SetMode(gin.DebugMode)
	}

	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	h := &Handler{producer: producer, simulator: sim}

	// Health check (used by Docker + Railway)
	r.GET("/health", h.Health)

	// API v1
	api := r.Group("/api")
	{
		api.POST("/products", h.AddProduct)
		api.GET("/products", h.ListProducts)
		api.GET("/products/:id", h.GetProduct)
		api.GET("/products/:id/history", h.GetPriceHistory)

		// Simulation controls (demo mode)
		api.POST("/simulate/flash-sale", h.SimFlashSale)
		api.POST("/simulate/out-of-stock", h.SimOutOfStock)
		api.POST("/simulate/competitor-drop", h.SimCompetitorDrop)
		api.POST("/simulate/fast-forward", h.SimFastForward)
	}

	return r
}

// ─────────────────────────────────────────
// Handlers
// ─────────────────────────────────────────

// Health godoc
// GET /health
func (h *Handler) Health(c *gin.Context) {
	// Quick DB ping
	if err := db.Pool.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"db":     err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}

// AddProduct godoc
// POST /api/products
// Body: { "url": "https://www.amazon.in/dp/..." }
// Smart flow:
//  1. Parse product name from URL slug
//  2. Try ScraperAPI for real price (if SCRAPER_API_KEY is set)
//  3. Fall back to simulated price based on keyword guess
func (h *Handler) AddProduct(c *gin.Context) {
	var req addProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "url is required"})
		return
	}

	url := strings.TrimSpace(req.URL)
	log.Printf("[AddProduct] Received URL: %s", url)

	// Detect platform from URL
	platform, err := detectPlatform(url)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	ctx := c.Request.Context()

	// Check duplicate URL (defensive)
	existing, _ := db.GetProductByURL(ctx, url)
	if existing != nil {
		log.Printf("[AddProduct] Duplicate URL check: product already exists in database: %s", existing.Name)
		c.JSON(200, gin.H{
			"product": existing,
			"message": "Already tracking this product",
		})
		return
	}

	// ── Step 1: Call FetchProductMetadata ───────────────────────────
	name, price, source, err := scraper.FetchProductMetadata(url)
	log.Printf("[AddProduct] Parsed name: %s", name)
	log.Printf("[AddProduct] Fetch result — price: %.2f, err: %v", price, err)

	var message string
	var basePrice float64

	if price > 0 {
		basePrice = price
		if source == "live" {
			message = "Live price fetched: ₹" + formatPrice(price)
		} else {
			message = "Price estimated from URL: ₹" + formatPrice(price)
		}
	} else {
		basePrice = guessPriceFromName(name)
		source = "simulated"
		message = "Could not fetch live price — using simulation"
	}

	// Upsert into DB with extracted name and source
	product, dbErr := db.InsertProduct(ctx, url, name, platform, source)
	log.Printf("[AddProduct] DB insert result — err: %v", dbErr)
	if dbErr != nil {
		log.Printf("[api] AddProduct db error: %v", dbErr)
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "failed to save product"})
		return
	}

	// Register product in simulator with guessed/real base price
	simCfg := &simulator.SimulatorConfig{
		Name:       name,
		URL:        url,
		Platform:   string(platform),
		Category:   simulator.CategoryElectronics,
		BasePrice:  basePrice,
		Volatility: simulator.VolatilityMedium,
	}
	h.simulator.RegisterProduct(product.ID.String(), simCfg)

	// Generate an initial price tick so the product has data immediately
	h.simulator.Tick(ctx, time.Now())

	// Publish scrape request to Kafka
	msg := appkafka.ScrapeRequestedMsg{
		ProductID: product.ID.String(),
		URL:       product.URL,
		Platform:  string(product.Platform),
		RequestAt: appkafka.TimeNow(),
	}
	if err := h.producer.PublishScrapeRequested(ctx, msg); err != nil {
		log.Printf("[api] AddProduct kafka error: %v", err)
	}

	c.JSON(http.StatusCreated, addProductResponse{
		Product:      *product,
		PriceSource:  product.PriceSource,
		FetchedPrice: price,
		Message:      message,
	})
}

// ListProducts godoc
// GET /api/products
// Returns all products enriched with latest price + buy/wait signal.
func (h *Handler) ListProducts(c *gin.Context) {
	ctx := c.Request.Context()

	products, err := db.GetProducts(ctx)
	if err != nil {
		log.Printf("[api] ListProducts db error: %v", err)
		c.JSON(http.StatusInternalServerError, errorResponse{Error: "failed to fetch products"})
		return
	}

	responses := make([]productResponse, 0, len(products))
	for _, p := range products {
		resp := enrichProduct(ctx, p)
		responses = append(responses, resp)
	}

	c.JSON(http.StatusOK, gin.H{
		"products": responses,
		"count":    len(responses),
	})
}

// GetProduct godoc
// GET /api/products/:id
func (h *Handler) GetProduct(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid product id"})
		return
	}

	ctx := c.Request.Context()
	product, err := db.GetProductByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, errorResponse{Error: "product not found"})
		return
	}

	c.JSON(http.StatusOK, enrichProduct(ctx, *product))
}

// GetPriceHistory godoc
// GET /api/products/:id/history?range=7d (or 30d, all)
func (h *Handler) GetPriceHistory(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "invalid product id"})
		return
	}

	// Parse range query param: default 7d
	rangeParam := c.DefaultQuery("range", "7d")

	ctx := c.Request.Context()

	var history []db.PriceHistory
	var stats *db.PriceStats

	if rangeParam == "all" {
		history, err = db.GetPriceHistoryAll(ctx, id)
		if err != nil {
			log.Printf("[api] GetPriceHistory(all) db error: %v", err)
			c.JSON(http.StatusInternalServerError, errorResponse{Error: "failed to fetch price history"})
			return
		}
		// Use 30d stats for ALL range (covers common window)
		stats, _ = db.GetPriceStats30d(ctx, id)
	} else {
		days, parseErr := parseDays(rangeParam)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, errorResponse{Error: "range must be 7d, 30d, or all"})
			return
		}
		since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)

		history, err = db.GetPriceHistory(ctx, id, since)
		if err != nil {
			log.Printf("[api] GetPriceHistory db error: %v", err)
			c.JSON(http.StatusInternalServerError, errorResponse{Error: "failed to fetch price history"})
			return
		}

		// Fetch stats for the window
		if days <= 7 {
			stats, _ = db.GetPriceStats7d(ctx, id)
		} else {
			stats, _ = db.GetPriceStats30d(ctx, id)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"product_id": id,
		"range":      rangeParam,
		"history":    history,
		"stats":      stats,
	})
}

// ─────────────────────────────────────────
// Simulation Control Handlers
// ─────────────────────────────────────────

// SimFlashSale triggers a flash sale event on iPhone 15.
// POST /api/simulate/flash-sale?product_id=xxx
func (h *Handler) SimFlashSale(c *gin.Context) {
	productID := c.Query("product_id")
	if productID == "" {
		// Default to iPhone 15
		productID = h.simulator.GetProductIDByName("iPhone 15")
	}
	if productID == "" {
		c.JSON(http.StatusNotFound, errorResponse{Error: "product not found"})
		return
	}

	if err := h.simulator.TriggerFlashSale(productID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	// Generate an immediate tick to reflect the change
	h.simulator.Tick(c.Request.Context(), time.Now())

	c.JSON(http.StatusOK, gin.H{
		"message":    "⚡ Flash sale triggered!",
		"product_id": productID,
	})
}

// SimOutOfStock triggers an out-of-stock event on Sony WH-1000XM5.
// POST /api/simulate/out-of-stock?product_id=xxx
func (h *Handler) SimOutOfStock(c *gin.Context) {
	productID := c.Query("product_id")
	if productID == "" {
		// Default to Sony headphones
		productID = h.simulator.GetProductIDByName("Sony WH-1000XM5 Headphones")
	}
	if productID == "" {
		c.JSON(http.StatusNotFound, errorResponse{Error: "product not found"})
		return
	}

	if err := h.simulator.TriggerOutOfStock(productID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	// Generate an immediate tick
	h.simulator.Tick(c.Request.Context(), time.Now())

	c.JSON(http.StatusOK, gin.H{
		"message":    "📦 Out of stock triggered!",
		"product_id": productID,
	})
}

// SimCompetitorDrop triggers a competitor price correlation effect.
// POST /api/simulate/competitor-drop?product_id=xxx
func (h *Handler) SimCompetitorDrop(c *gin.Context) {
	productID := c.Query("product_id")
	if productID == "" {
		// Default to iPhone 15 (will trigger Samsung Galaxy S24 competitor drop)
		productID = h.simulator.GetProductIDByName("iPhone 15")
	}
	if productID == "" {
		c.JSON(http.StatusNotFound, errorResponse{Error: "product not found"})
		return
	}

	if err := h.simulator.TriggerCompetitorDrop(productID); err != nil {
		c.JSON(http.StatusInternalServerError, errorResponse{Error: err.Error()})
		return
	}

	// Generate an immediate tick to apply the competitor drop
	h.simulator.Tick(c.Request.Context(), time.Now())

	c.JSON(http.StatusOK, gin.H{
		"message":    "🔄 Competitor drop triggered!",
		"product_id": productID,
	})
}

// SimFastForward generates N hours of price data instantly.
// POST /api/simulate/fast-forward?hours=24
func (h *Handler) SimFastForward(c *gin.Context) {
	hoursStr := c.DefaultQuery("hours", "24")
	hours, err := strconv.Atoi(hoursStr)
	if err != nil || hours < 1 || hours > 168 {
		c.JSON(http.StatusBadRequest, errorResponse{Error: "hours must be between 1 and 168"})
		return
	}

	ticks := h.simulator.FastForward(c.Request.Context(), hours)

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("⏩ Fast-forwarded %d hours (%d data points generated)", hours, ticks),
		"hours":   hours,
		"ticks":   ticks,
	})
}

// ─────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────

// enrichProduct augments a Product with live price data + buy/wait signal.
func enrichProduct(ctx context.Context, p db.Product) productResponse {
	resp := productResponse{Product: p}

	// Latest price
	latest, err := db.GetLatestPrice(ctx, p.ID)
	if err == nil {
		resp.CurrentPrice = latest.Price
		resp.Currency = latest.Currency
		resp.InStock = latest.InStock
		resp.LastScraped = &latest.ScrapedAt
	}

	// 7-day stats for buy/wait signal
	stats7d, err := db.GetPriceStats7d(ctx, p.ID)
	if err == nil && stats7d.DataPoints > 1 {
		avg := stats7d.AvgPrice
		if resp.CurrentPrice < avg {
			resp.Signal = "BUY"
		} else {
			resp.Signal = "WAIT"
		}
		// % change from 7d average
		if avg > 0 {
			resp.PriceChange = ((resp.CurrentPrice - avg) / avg) * 100
		}
	} else {
		resp.Signal = "INSUFFICIENT_DATA"
	}

	// 30-day lowest check
	isLowest, err := db.IsLowestIn30Days(ctx, p.ID, resp.CurrentPrice)
	if err == nil {
		resp.IsLowest30d = isLowest
	}

	// Recent prices for sparkline (last 10 ticks)
	history, err := db.GetPriceHistory(ctx, p.ID, time.Now().Add(-10*24*time.Hour))
	if err == nil {
		recentPrices := make([]float64, 0, len(history))
		for _, h := range history {
			recentPrices = append(recentPrices, h.Price)
		}
		if len(recentPrices) > 10 {
			recentPrices = recentPrices[len(recentPrices)-10:]
		}
		resp.RecentPrices = recentPrices
	}

	return resp
}

// detectPlatform infers the platform from the URL.
func detectPlatform(rawURL string) (db.Platform, error) {
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, "amazon.in") || strings.Contains(lower, "amazon.com"):
		return db.PlatformAmazon, nil
	case strings.Contains(lower, "flipkart.com"):
		return db.PlatformFlipkart, nil
	default:
		return "", fmt.Errorf("unsupported platform — only Amazon and Flipkart URLs are supported")
	}
}

// extractProductName parses the URL slug to get a human-readable product name.
// Amazon: /Apple-iPhone-15-128GB-Black/dp/B0CHX1W1XY → "Apple iPhone 15 128GB Black"
// Flipkart: /apple-iphone-15-blue-128-gb/p/itm... → "Apple Iphone 15 Blue 128 Gb"
func extractProductName(rawURL string) string {
	// Amazon: extract slug before /dp/
	amazonRe := regexp.MustCompile(`/([^/]+)/dp/`)
	if m := amazonRe.FindStringSubmatch(rawURL); len(m) >= 2 {
		slug := m[1]
		// Replace hyphens and underscores with spaces, then title-case
		slug = strings.NewReplacer("-", " ", "_", " ").Replace(slug)
		return toTitleCase(slug)
	}

	// Flipkart: first path segment after domain
	flipcartRe := regexp.MustCompile(`flipkart\.com/([^/]+)/`)
	if m := flipcartRe.FindStringSubmatch(rawURL); len(m) >= 2 {
		slug := m[1]
		slug = strings.NewReplacer("-", " ", "_", " ").Replace(slug)
		return toTitleCase(slug)
	}

	// Last fallback: parse URL path, use last meaningful segment
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "Unknown Product"
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if len(segments[i]) > 4 {
			slug := strings.NewReplacer("-", " ", "_", " ").Replace(segments[i])
			return toTitleCase(slug)
		}
	}
	return "Unknown Product"
}

// toTitleCase converts a slug like "apple iphone 15" to "Apple Iphone 15".
func toTitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}

// guessPriceFromName estimates a base price (INR) based on product name keywords.
func guessPriceFromName(name string) float64 {
	lower := strings.ToLower(name)
	switch {
	case containsAny(lower, "iphone", "samsung galaxy", "pixel", "oneplus"):
		return 60000
	case containsAny(lower, "headphone", "earbuds", "airpods", "earphone", "headset"):
		return 20000
	case containsAny(lower, "tv", "television", "smart tv", "oled", "qled"):
		return 45000
	case containsAny(lower, "shoe", "nike", "adidas", "puma", "sneaker", "boot"):
		return 8000
	case containsAny(lower, "laptop", "macbook", "notebook"):
		return 70000
	case containsAny(lower, "tablet", "ipad"):
		return 35000
	case containsAny(lower, "camera", "dslr", "mirrorless"):
		return 50000
	case containsAny(lower, "watch", "smartwatch", "band"):
		return 15000
	default:
		return 15000
	}
}

func containsAny(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// FetchRealPrice fetches the actual product price via ScraperAPI.
// It parses the HTML response to find the price element for Amazon/Flipkart.
func FetchRealPrice(productURL, apiKey string) (float64, error) {
	scraperURL := fmt.Sprintf(
		"https://api.scraperapi.com?api_key=%s&url=%s",
		apiKey, url.QueryEscape(productURL),
	)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(scraperURL)
	if err != nil {
		return 0, fmt.Errorf("ScraperAPI request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ScraperAPI returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read ScraperAPI response: %w", err)
	}

	price, err := parsePriceFromHTML(string(body), productURL)
	if err != nil {
		return 0, fmt.Errorf("price parsing failed: %w", err)
	}
	return price, nil
}

// parsePriceFromHTML extracts the price from the HTML response body.
// Supports Amazon (span.a-price-whole) and Flipkart (div._30jeq3).
func parsePriceFromHTML(htmlStr, productURL string) (float64, error) {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return 0, fmt.Errorf("HTML parse error: %w", err)
	}

	isAmazon := strings.Contains(strings.ToLower(productURL), "amazon")

	var priceStr string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if priceStr != "" {
			return
		}
		if n.Type == html.ElementNode {
			classes := getAttr(n, "class")
			if isAmazon && n.Data == "span" && strings.Contains(classes, "a-price-whole") {
				if n.FirstChild != nil {
					priceStr = strings.TrimSpace(n.FirstChild.Data)
				}
			} else if !isAmazon && n.Data == "div" && strings.Contains(classes, "_30jeq3") {
				if n.FirstChild != nil {
					priceStr = strings.TrimSpace(n.FirstChild.Data)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	if priceStr == "" {
		return 0, fmt.Errorf("price element not found in HTML")
	}

	// Clean: remove ₹, commas, spaces, dots after thousands
	priceStr = strings.NewReplacer("₹", "", ",", "", ".", "", " ", "").Replace(priceStr)
	priceStr = strings.TrimSpace(priceStr)

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse price %q: %w", priceStr, err)
	}
	return price, nil
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// parseDays converts "7d" → 7, "30d" → 30.
func parseDays(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "d")
	n, err := strconv.Atoi(s)
	if err != nil || (n != 7 && n != 30) {
		return 0, fmt.Errorf("invalid range: %s", s)
	}
	return n, nil
}

// corsMiddleware sets CORS headers to allow the Vercel frontend.
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")

		allowedOrigins := []string{
			"http://localhost:5173",   // Vite dev
			"http://localhost:3000",   // CRA dev
		}

		// In production, also allow the Vercel domain from env
		if vercelURL := os.Getenv("FRONTEND_URL"); vercelURL != "" {
			allowedOrigins = append(allowedOrigins, vercelURL)
		}

		for _, allowed := range allowedOrigins {
			if origin == allowed {
				c.Header("Access-Control-Allow-Origin", origin)
				break
			}
		}

		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func formatPrice(p float64) string {
	in := strconv.FormatFloat(p, 'f', 0, 64)
	if len(in) <= 3 {
		return in
	}
	var sb strings.Builder
	n := len(in)
	for i, char := range in {
		if i > 0 && (n-i)%3 == 0 {
			sb.WriteRune(',')
		}
		sb.WriteRune(char)
	}
	return sb.String()
}
