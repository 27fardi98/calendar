package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// ── FF Calendar structs ──────────────────────────────────────────

type FFEvent struct {
	Title    string `json:"title"`
	Country  string `json:"country"`
	Date     string `json:"date"`
	Impact   string `json:"impact"`
	Forecast string `json:"forecast"`
	Previous string `json:"previous"`
	Actual   string `json:"actual"`
	Currency string `json:"currency"`
}

type CalendarResponse struct {
	Source    string    `json:"source"`
	FetchedAt string    `json:"fetched_at"`
	Count     int       `json:"count"`
	Filter    Filter    `json:"filter"`
	Events    []FFEvent `json:"events"`
}

type Filter struct {
	Currency string `json:"currency"`
	Impact   string `json:"impact"`
}

// ── Fetch & filter ───────────────────────────────────────────────

const ffURL = "https://nfs.faireconomy.media/ff_calendar_thisweek.json"

var httpClient = &http.Client{Timeout: 10 * time.Second}

func fetchAndFilter(currency, impact string) ([]FFEvent, error) {
	req, _ := http.NewRequest("GET", ffURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (forex-calendar-api/1.0)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}

	var events []FFEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}

	currency = strings.ToUpper(currency)
	impact = strings.ToLower(impact)

	var filtered []FFEvent
	for _, e := range events {
		matchCurrency := currency == "" || strings.ToUpper(e.Country) == currency
		matchImpact := impact == "" || strings.ToLower(e.Impact) == impact
		if matchCurrency && matchImpact {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ── Handlers ─────────────────────────────────────────────────────

// GET /calendar?currency=USD&impact=high
func calendarHandler(c *fiber.Ctx) error {
	currency := c.Query("currency", "USD")
	impact := c.Query("impact", "High")

	events, err := fetchAndFilter(currency, impact)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(CalendarResponse{
		Source:    ffURL,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
		Count:     len(events),
		Filter:    Filter{Currency: strings.ToUpper(currency), Impact: impact},
		Events:    events,
	})
}

// GET /health
func healthHandler(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// ── Main ─────────────────────────────────────────────────────────

func main() {
	app := fiber.New(fiber.Config{
		AppName: "Forex Calendar API v1.0",
	})

	app.Use(recover.New())
	app.Use(logger.New())

	app.Get("/calendar", calendarHandler)
	app.Get("/health", healthHandler)

	port := ":8080"
	log.Printf("Forex Calendar API running on http://localhost%s", port)
	log.Printf("Endpoints:")
	log.Printf("  GET /calendar            → USD High Impact (default)")
	log.Printf("  GET /calendar?currency=EUR&impact=medium")
	log.Printf("  GET /health")
	log.Fatal(app.Listen(port))
}
