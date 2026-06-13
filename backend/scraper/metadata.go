package scraper

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// FetchProductMetadata fetches lightweight product metadata (JSON-LD, Open Graph, fallbacks).
func FetchProductMetadata(productURL string) (name string, price float64, source string, err error) {
	// CAUSE A — Metadata fetch panics on nil response:
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Scraper] Panic recovered: %v", r)
			err = fmt.Errorf("scraper panic: %v", r)
		}
	}()

	// Step 1: Delay and User-Agent rotation
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36",
	}

	randomUserAgent := userAgents[rand.Intn(len(userAgents))]

	// 2-5 seconds random delay
	time.Sleep(time.Duration(2000+rand.Intn(3000)) * time.Millisecond)

	// CAUSE B — HTTP client has no timeout and hangs:
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	// LAYER 1: Try ScraperAPI (if key is set in .env)
	var requestURL string
	apiKey := os.Getenv("SCRAPER_API_KEY")
	if apiKey != "" {
		requestURL = fmt.Sprintf("https://api.scraperapi.com/?api_key=%s&url=%s&render=false", apiKey, url.QueryEscape(productURL))
	} else {
		requestURL = productURL
	}

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		price, _ = EstimatePriceFromURL(productURL)
		name = cleanProductName(parseNameFromURL(productURL))
		return name, price, "estimated", fmt.Errorf("metadata: failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", randomUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-IN,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("DNT", "1")

	resp, err := client.Do(req)
	if err != nil {
		price, _ = EstimatePriceFromURL(productURL)
		name = cleanProductName(parseNameFromURL(productURL))
		return name, price, "estimated", nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		price, _ = EstimatePriceFromURL(productURL)
		name = cleanProductName(parseNameFromURL(productURL))
		return name, price, "estimated", nil
	}

	// Handle GZIP decompression if header specifies
	var bodyReader io.Reader = resp.Body
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Encoding")), "gzip") {
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			price, _ = EstimatePriceFromURL(productURL)
			name = cleanProductName(parseNameFromURL(productURL))
			return name, price, "estimated", nil
		}
		defer gzipReader.Close()
		bodyReader = gzipReader
	}

	bodyBytes, err := io.ReadAll(bodyReader)
	if err != nil {
		price, _ = EstimatePriceFromURL(productURL)
		name = cleanProductName(parseNameFromURL(productURL))
		return name, price, "estimated", nil
	}
	htmlStr := string(bodyBytes)

	lowerURL := strings.ToLower(productURL)
	isAmazon := strings.Contains(lowerURL, "amazon.in") || strings.Contains(lowerURL, "amazon.com")
	isFlipkart := strings.Contains(lowerURL, "flipkart.com")

	// Parse HTML structure
	parsedData := parseHTML(htmlStr, isAmazon, isFlipkart)

	// Step 2: Try JSON-LD first
	for _, ldStr := range parsedData.JSONLDs {
		var ldJSON any
		if err := json.Unmarshal([]byte(ldStr), &ldJSON); err == nil {
			n, p := findProductOffers(ldJSON)
			if n != "" && name == "" {
				name = n
			}
			if p > 0 && price == 0 {
				price = p
			}
		}
	}

	// Step 3: Try Open Graph tags if fields are still empty
	if name == "" {
		if ogTitle, ok := parsedData.MetaTags["og:title"]; ok {
			name = strings.TrimSpace(ogTitle)
		}
	}
	if price == 0 {
		if pVal, ok := parsedData.MetaTags["product:price:amount"]; ok {
			price = cleanAndParsePrice(pVal)
		} else if pVal, ok := parsedData.MetaTags["og:price:amount"]; ok {
			price = cleanAndParsePrice(pVal)
		}
	}

	// Step 4 & 5: Platform-specific fallbacks
	if name == "" {
		if isAmazon && parsedData.AmazonTitle != "" {
			name = parsedData.AmazonTitle
		} else if isFlipkart && parsedData.FlipkartTitle != "" {
			name = parsedData.FlipkartTitle
		}
	}
	if price == 0 {
		if isAmazon && parsedData.AmazonPrice != "" {
			price = cleanAndParsePrice(parsedData.AmazonPrice)
		} else if isFlipkart && parsedData.FlipkartPrice != "" {
			price = cleanAndParsePrice(parsedData.FlipkartPrice)
		}
	}

	// Determine if we found the price live
	if price > 0 {
		source = "live"
	} else {
		// LAYER 3: Smart price estimation from URL
		price, _ = EstimatePriceFromURL(productURL)
		source = "estimated"
	}

	// Step 6: URL slug fallback for name
	if name == "" {
		name = parseNameFromURL(productURL)
	}

	// Clean name
	name = cleanProductName(name)

	return name, price, source, nil
}

// EstimatePriceFromURL performs smart estimation of price and categorisation based on URL keywords.
func EstimatePriceFromURL(rawURL string) (float64, string) {
	lower := strings.ToLower(rawURL)

	// Phone detection
	phoneKeywords := []string{
		"iphone", "samsung-galaxy", "pixel-", "oneplus",
		"redmi-note", "poco-x", "realme-",
	}
	// Premium phones
	premiumPhones := []string{"iphone-15", "iphone-14",
		"s24-ultra", "s23-ultra", "pixel-8-pro"}
	for _, kw := range premiumPhones {
		if strings.Contains(lower, kw) {
			return 79999 + float64(rand.Intn(10000)), "electronics"
		}
	}
	// Mid-range phones  
	for _, kw := range phoneKeywords {
		if strings.Contains(lower, kw) {
			return 24999 + float64(rand.Intn(25000)), "electronics"
		}
	}

	// Laptop detection
	laptopKeywords := []string{"laptop", "macbook", "thinkpad",
		"dell-xps", "asus-rog", "lenovo-ideapad"}
	premiumLaptops := []string{"macbook-pro", "dell-xps-15",
		"asus-rog-zephyrus"}
	for _, kw := range premiumLaptops {
		if strings.Contains(lower, kw) {
			return 129999 + float64(rand.Intn(50000)), "electronics"
		}
	}
	for _, kw := range laptopKeywords {
		if strings.Contains(lower, kw) {
			return 54999 + float64(rand.Intn(35000)), "electronics"
		}
	}

	// TV detection
	tvKeywords := []string{"television", "-tv-", "smart-tv",
		"oled-tv", "qled"}
	for _, kw := range tvKeywords {
		if strings.Contains(lower, kw) {
			// Check for screen size hints
			if strings.Contains(lower, "65") || strings.Contains(lower, "75") {
				return 89999 + float64(rand.Intn(40000)), "appliances"
			}
			return 44999 + float64(rand.Intn(30000)), "appliances"
		}
	}

	// Headphone/Audio detection
	audioKeywords := []string{"headphone", "earphone", "earbuds",
		"airpods", "wh-1000", "wf-1000", "soundbar"}
	premiumAudio := []string{"airpods-pro", "wh-1000xm5",
		"wh-1000xm4", "bose-qc"}
	for _, kw := range premiumAudio {
		if strings.Contains(lower, kw) {
			return 24999 + float64(rand.Intn(10000)), "electronics"
		}
	}
	for _, kw := range audioKeywords {
		if strings.Contains(lower, kw) {
			return 2999 + float64(rand.Intn(12000)), "electronics"
		}
	}

	// Shoes/Fashion
	shoeKeywords := []string{"shoes", "sneakers", "nike",
		"adidas", "puma", "skechers", "reebok"}
	for _, kw := range shoeKeywords {
		if strings.Contains(lower, kw) {
			return 2999 + float64(rand.Intn(8000)), "fashion"
		}
	}

	// Appliances
	applianceKeywords := []string{"washing-machine", "refrigerator",
		"fridge", "microwave", "air-conditioner", "ac-"}
	for _, kw := range applianceKeywords {
		if strings.Contains(lower, kw) {
			return 19999 + float64(rand.Intn(40000)), "appliances"
		}
	}

	// Books
	bookKeywords := []string{"book", "paperback", "hardcover"}
	for _, kw := range bookKeywords {
		if strings.Contains(lower, kw) {
			return 299 + float64(rand.Intn(700)), "books"
		}
	}

	// Default: general electronics
	return 9999 + float64(rand.Intn(10000)), "electronics"
}

// cleanProductName strips noise patterns and formats the product name.
func cleanProductName(raw string) string {
	// Remove common URL noise patterns
	noisePatterns := []string{
		// Size codes like "uk11", "uk-11", "size-42"
		`(?i)\buk\d+\b`,
		`(?i)\bsize-?\d+\b`,
		// Color codes and SKU-like patterns
		`(?i)\b[a-z]{2,4}\d{4,}\b`,
		// Extra adjectives that are clearly SEO spam
		`(?i)\b(summits|brisbane|casual)\b`,
		// Trailing/leading junk
		`^\s+|\s+$`,
		// Multiple spaces
		`\s{2,}`,
	}

	result := raw
	for _, pattern := range noisePatterns {
		re := regexp.MustCompile(pattern)
		result = re.ReplaceAllString(result, " ")
	}

	// Title case using strings.Title (requested)
	result = strings.Title(strings.ToLower(strings.TrimSpace(result)))

	// Truncate to 60 chars at word boundary
	if len(result) > 60 {
		result = result[:60]
		lastSpace := strings.LastIndex(result, " ")
		if lastSpace > 40 {
			result = result[:lastSpace]
		}
	}

	return result
}

// ScrapingResult holds parsed nodes from a single HTML pass.
type ScrapingResult struct {
	JSONLDs       []string
	MetaTags      map[string]string
	AmazonTitle   string
	AmazonPrice   string
	FlipkartTitle string
	FlipkartPrice string
}

func parseHTML(htmlStr string, isAmazon, isFlipkart bool) ScrapingResult {
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return ScrapingResult{MetaTags: make(map[string]string)}
	}

	res := ScrapingResult{
		MetaTags: make(map[string]string),
	}

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "script" {
				if getAttr(n, "type") == "application/ld+json" {
					res.JSONLDs = append(res.JSONLDs, getNodeText(n))
				}
			} else if n.Data == "meta" {
				prop := getAttr(n, "property")
				nameAttr := getAttr(n, "name")
				content := getAttr(n, "content")
				if content != "" {
					key := prop
					if key == "" {
						key = nameAttr
					}
					if key != "" {
						res.MetaTags[strings.ToLower(key)] = content
					}
				}
			} else if isAmazon {
				if n.Data == "span" && getAttr(n, "id") == "productTitle" {
					res.AmazonTitle = getNodeText(n)
				} else if n.Data == "span" && strings.Contains(getAttr(n, "class"), "a-price-whole") {
					if res.AmazonPrice == "" {
						res.AmazonPrice = getNodeText(n)
					}
				}
			} else if isFlipkart {
				classes := getAttr(n, "class")
				if n.Data == "span" && strings.Contains(classes, "B_NuCI") {
					res.FlipkartTitle = getNodeText(n)
				} else if n.Data == "div" && strings.Contains(classes, "_30jeq3") {
					if res.FlipkartPrice == "" {
						res.FlipkartPrice = getNodeText(n)
					}
				}
			}
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return res
}

func getAttr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func getNodeText(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			sb.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.TrimSpace(sb.String())
}

func findProductOffers(data any) (name string, price float64) {
	switch v := data.(type) {
	case map[string]any:
		if t, ok := v["@type"].(string); ok && t == "Product" {
			if n, ok := v["name"].(string); ok {
				name = n
			}
			if offers, ok := v["offers"].(map[string]any); ok {
				if p := parsePriceVal(offers["price"]); p > 0 {
					price = p
				} else if p := parsePriceVal(offers["lowPrice"]); p > 0 {
					price = p
				}
			}
			if name != "" || price > 0 {
				return name, price
			}
		}
		// Search nested maps
		for _, val := range v {
			if n, p := findProductOffers(val); n != "" || p > 0 {
				if n != "" && name == "" {
					name = n
				}
				if p > 0 && price == 0 {
					price = p
				}
				if name != "" && price > 0 {
					return name, price
				}
			}
		}
	case []any:
		for _, item := range v {
			if n, p := findProductOffers(item); n != "" || p > 0 {
				if n != "" && name == "" {
					name = n
				}
				if p > 0 && price == 0 {
					price = p
				}
				if name != "" && price > 0 {
					return name, price
				}
			}
		}
	}
	return name, price
}

func parsePriceVal(val any) float64 {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		return cleanAndParsePrice(v)
	}
	return 0
}

func cleanAndParsePrice(s string) float64 {
	s = strings.NewReplacer("₹", "", ",", "", " ", "").Replace(s)
	re := regexp.MustCompile(`\d+(\.\d+)?`)
	match := re.FindString(s)
	if match == "" {
		return 0
	}
	val, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0
	}
	return val
}

func parseNameFromURL(rawURL string) string {
	// Amazon: extract slug before /dp/
	amazonRe := regexp.MustCompile(`/([^/]+)/dp/`)
	if m := amazonRe.FindStringSubmatch(rawURL); len(m) >= 2 {
		slug := m[1]
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

	// Fallback path parse
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

func toTitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		}
	}
	return strings.Join(words, " ")
}
