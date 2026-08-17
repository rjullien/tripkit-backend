package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/rjullien/tripkit-backend/internal/dailybrief"
	"github.com/rjullien/tripkit-backend/internal/models"
	"github.com/rjullien/tripkit-backend/internal/weather"
)

// GetWeather returns a normalized forecast for a trip location.
// GET /trips/{tripId}/weather?lat=X&lon=X
// The provider is selected based on the trip's country field (US→NWS, CA→MSC, default→Open-Meteo).
func (h *Handler) GetWeather(w http.ResponseWriter, r *http.Request) {
	tripID := chi.URLParam(r, "tripId")

	lat := r.URL.Query().Get("lat")
	lon := r.URL.Query().Get("lon")
	if lat == "" || lon == "" {
		writeError(w, http.StatusBadRequest, "lat and lon query parameters are required")
		return
	}

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

	// Look up trip to get country.
	var trip models.Trip
	if err := h.db.First(&trip, "id = ?", tripID).Error; err != nil {
		writeError(w, http.StatusNotFound, "Trip not found")
		return
	}

	country := ""
	if trip.Data != nil {
		var data map[string]any
		if err := json.Unmarshal([]byte(*trip.Data), &data); err == nil {
			if c, ok := data["country"].(string); ok {
				country = c
			}
		}
	}

	fc, err := h.weather.GetForecast(weather.ForecastRequest{
		Lat:     latF,
		Lon:     lonF,
		Country: country,
		Days:    7,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "Weather fetch failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, fc)
}

// GetWeatherForecast is a standalone endpoint for the frontend.
// GET /weather/forecast?lat=X&lon=X&country=XX&days=7&date=2006-01-02&tz=America/Montreal
// No trip lookup needed — country is passed explicitly by the client.
func (h *Handler) GetWeatherForecast(w http.ResponseWriter, r *http.Request) {
	lat := r.URL.Query().Get("lat")
	lon := r.URL.Query().Get("lon")
	if lat == "" || lon == "" {
		writeError(w, http.StatusBadRequest, "lat and lon query parameters are required")
		return
	}

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

	country := r.URL.Query().Get("country")
	tz := r.URL.Query().Get("tz")
	date := r.URL.Query().Get("date")

	days := 7
	if d := r.URL.Query().Get("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 && v <= 16 {
			days = v
		}
	}

	fc, err := h.weather.GetForecast(weather.ForecastRequest{
		Lat:      latF,
		Lon:      lonF,
		Country:  country,
		Days:     days,
		Date:     date,
		Timezone: tz,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "Weather fetch failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, fc)
}


// weatherProvider returns the weather adapter that satisfies dailybrief.WeatherProvider.
// Returns nil if the weather service is not configured (graceful degradation).
func (h *Handler) weatherProvider() dailybrief.WeatherProvider {
	if h.weather == nil {
		return nil
	}
	return &weather.DailyBriefAdapter{Svc: h.weather}
}
