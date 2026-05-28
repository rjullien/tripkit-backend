package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/models"
)

// GetWeather proxies weather data from the appropriate provider based on trip country.
// GET /trips/:id/weather?lat=X&lon=X
func (h *Handler) GetWeather(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	lat := r.URL.Query().Get("lat")
	lon := r.URL.Query().Get("lon")
	if lat == "" || lon == "" {
		writeError(w, http.StatusBadRequest, "lat and lon query parameters are required")
		return
	}

	// Validate lat/lon are valid numbers
	latF, err := strconv.ParseFloat(lat, 64)
	if err != nil || latF < -90 || latF > 90 {
		writeError(w, http.StatusBadRequest, "lat must be a number between -90 and 90")
		return
	}
	lonF, err := strconv.ParseFloat(lon, 64)
	if err != nil || lonF < -180 || lonF > 180 {
		writeError(w, http.StatusBadRequest, "lon must be a number between -180 and 180")
		return
	}
	// Use validated formatted values to avoid injection
	lat = strconv.FormatFloat(latF, 'f', -1, 64)
	lon = strconv.FormatFloat(lonF, 'f', -1, 64)

	// Look up trip
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	// Extract country from trip data JSON
	country := ""
	if trip.Data != nil {
		var data map[string]any
		if err := json.Unmarshal([]byte(*trip.Data), &data); err == nil {
			if c, ok := data["country"].(string); ok {
				country = c
			}
		}
	}

	// Route to correct provider
	var provider string
	var apiURL string

	switch country {
	case "US":
		provider = "nws"
		// NWS requires a 2-step call: points → forecast
		apiURL = fmt.Sprintf("https://api.weather.gov/points/%s,%s", lat, lon)
	case "CA":
		provider = "msc"
		apiURL = fmt.Sprintf("https://api.weather.gc.ca/collections/weather:forecasts/items?lat=%s&lon=%s&limit=7", lat, lon)
	default:
		provider = "open-meteo"
		apiURL = fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s&daily=temperature_2m_max,temperature_2m_min,precipitation_sum,weathercode&timezone=auto&forecast_days=7", lat, lon)
	}

	client := &http.Client{Timeout: 10 * time.Second}

	if provider == "nws" {
		// NWS: 2-step — get the forecast URL first
		data, err := fetchJSON(client, apiURL, map[string]string{
			"User-Agent": "TripKit/1.0 (tripkit.bapttf.com)",
			"Accept":     "application/geo+json",
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("NWS points lookup failed: %v", err))
			return
		}

		// Extract forecast URL from response
		props, ok := data["properties"].(map[string]any)
		if !ok {
			writeError(w, http.StatusBadGateway, "NWS response missing properties")
			return
		}
		forecastURL, ok := props["forecast"].(string)
		if !ok || forecastURL == "" {
			writeError(w, http.StatusBadGateway, "NWS response missing forecast URL")
			return
		}

		// Fetch the actual forecast
		forecast, err := fetchJSON(client, forecastURL, map[string]string{
			"User-Agent": "TripKit/1.0 (tripkit.bapttf.com)",
			"Accept":     "application/geo+json",
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("NWS forecast fetch failed: %v", err))
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"provider": provider,
			"data":     forecast,
		})
		return
	}

	// MSC and Open-Meteo: single call
	headers := map[string]string{}
	if provider == "msc" {
		headers["Accept"] = "application/geo+json"
	}

	data, err := fetchJSON(client, apiURL, headers)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("Weather fetch failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider": provider,
		"data":     data,
	})
}

// fetchJSON performs an HTTP GET and decodes the JSON response.
func fetchJSON(client *http.Client, url string, headers map[string]string) (map[string]any, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	return result, nil
}
