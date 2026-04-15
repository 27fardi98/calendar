package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// ── FF Calendar structs ──────────────────────────────────────────

// FFEvent mirrors FF JSON exactly (country = currency code in FF)
type FFEvent struct {
	Title    string `json:"title"`
	Country  string `json:"country"` // FF uses "country" = "USD", "EUR", etc.
	Date     string `json:"date"`
	Impact   string `json:"impact"`
	Forecast string `json:"forecast"`
	Previous string `json:"previous"`
	Actual   string `json:"actual"`
}

// OutputEvent is what we return — "country" renamed to "currency"
type OutputEvent struct {
	Title    string `json:"title"`
	Currency string `json:"currency"` // FIX 1: mapped from FFEvent.Country
	Date     string `json:"date"`
	Impact   string `json:"impact"`
	Forecast string `json:"forecast"`
	Previous string `json:"previous"`
	Actual   string `json:"actual"`
}

type CalendarResponse struct {
	Source    string        `json:"source"`
	FetchedAt string        `json:"fetched_at"`
	CachedAt  string        `json:"cached_at"`
	Count     int           `json:"count"`
	Filter    Filter        `json:"filter"`
	Events    []OutputEvent `json:"events"`
}

type Filter struct {
	Currency string `json:"currency"`
	Impact   string `json:"impact"`
}

// ── FIX 2: Cache ─────────────────────────────────────────────────

const cacheTTL = 30 * time.Minute

type cache struct {
	mu       sync.RWMutex
	data     []FFEvent
	cachedAt time.Time
}

var store = &cache{}

func (c *cache) get() ([]FFEvent, time.Time, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if time.Since(c.cachedAt) < cacheTTL && len(c.data) > 0 {
		return c.data, c.cachedAt, true
	}
	return nil, time.Time{}, false
}

func (c *cache) set(data []FFEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data = data
	c.cachedAt = time.Now()
}

// ── Fetch & filter ───────────────────────────────────────────────

const ffURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func fetchFromFF() ([]FFEvent, time.Time, error) {
	// Return cache if still valid
	if data, cachedAt, ok := store.get(); ok {
		return data, cachedAt, nil
	}

	req, _ := http.NewRequest("GET", ffURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (forex-calendar-api/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, time.Time{}, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}

	var events []FFEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode failed: %w", err)
	}

	store.set(events)
	return events, time.Now(), nil
}

func fetchAndFilter(currency, impact string) ([]OutputEvent, time.Time, error) {
	events, cachedAt, err := fetchFromFF()
	if err != nil {
		return nil, time.Time{}, err
	}

	currency = strings.ToUpper(currency)
	impact = strings.ToLower(impact)

	var filtered []OutputEvent
	for _, e := range events {
		matchCurrency := currency == "" || strings.ToUpper(e.Country) == currency
		matchImpact := impact == "" || strings.ToLower(e.Impact) == impact
		if matchCurrency && matchImpact {
			filtered = append(filtered, OutputEvent{
				Title:    e.Title,
				Currency: e.Country, // FIX 1: remap field
				Date:     e.Date,
				Impact:   e.Impact,
				Forecast: e.Forecast,
				Previous: e.Previous,
				Actual:   e.Actual,
			})
		}
	}
	return filtered, cachedAt, nil
}

// ── Handlers ─────────────────────────────────────────────────────

// GET /calendar?currency=USD&impact=high
func calendarHandler(c *fiber.Ctx) error {
	currency := c.Query("currency", "USD")
	impact := c.Query("impact", "High")

	events, cachedAt, err := fetchAndFilter(currency, impact)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(CalendarResponse{
		Source:    ffURL,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		CachedAt:  cachedAt.UTC().Format(time.RFC3339),
		Count:     len(events),
		Filter:    Filter{Currency: strings.ToUpper(currency), Impact: impact},
		Events:    events,
	})
}

// FIX 3: GET /health — check upstream FF too
func healthHandler(c *fiber.Ctx) error {
	upstreamOK := true
	upstreamMsg := "ok"

	req, _ := http.NewRequest("HEAD", ffURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (forex-calendar-api/1.0)")
	resp, err := httpClient.Do(req)
	if err != nil {
		upstreamOK = false
		upstreamMsg = err.Error()
	} else {
		resp.Body.Close()
		if resp.StatusCode != 200 {
			upstreamOK = false
			upstreamMsg = fmt.Sprintf("status %d", resp.StatusCode)
		}
	}

	_, cachedAt, cacheHit := store.get()
	cacheStatus := "miss"
	cacheAge := ""
	if cacheHit {
		cacheStatus = "hit"
		cacheAge = fmt.Sprintf("%.0fs", time.Since(cachedAt).Seconds())
	}

	status := fiber.StatusOK
	if !upstreamOK {
		status = fiber.StatusServiceUnavailable
	}

	return c.Status(status).JSON(fiber.Map{
		"status": fiber.Map{
			"api":      "ok",
			"upstream": upstreamOK,
		},
		"upstream_message": upstreamMsg,
		"cache": fiber.Map{
			"status":    cacheStatus,
			"age":       cacheAge,
			"ttl":       cacheTTL.String(),
			"cached_at": cachedAt.UTC().Format(time.RFC3339),
		},
		"time": time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	app := fiber.New(fiber.Config{
		AppName: "Forex Calendar API v1.1",
	})

	app.Use(recover.New())
	app.Use(logger.New())

	app.Get("/calendar", calendarHandler)
	app.Get("/health", healthHandler)

	port := ":8001"
	log.Printf("Forex Calendar API running on http://localhost%s", port)
	log.Printf("  GET /calendar            → USD High Impact (default)")
	log.Printf("  GET /calendar?currency=EUR&impact=medium")
	log.Printf("  GET /health              → includes upstream + cache status")
	log.Fatal(app.Listen(port))
}
