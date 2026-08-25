package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"time"

	"momo-poo/internal/ratelimit"
	"momo-poo/internal/trips"
	"momo-poo/web"
)

const maxLookbackDays = 366

type tripStore interface {
	Create(context.Context, bool) (trips.Trip, error)
	List(context.Context, time.Time, time.Time) ([]trips.Trip, error)
	Ping(context.Context) error
}

type app struct {
	store        tripStore
	location     *time.Location
	writeLimiter *ratelimit.Limiter
	readLimiter  *ratelimit.Limiter
	now          func() time.Time
}

func newApp(store tripStore, location *time.Location, writeLimiter, readLimiter *ratelimit.Limiter) *app {
	return &app{store: store, location: location, writeLimiter: writeLimiter, readLimiter: readLimiter, now: time.Now}
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("POST /trips", a.createTripForm)
	mux.HandleFunc("GET /history", a.history)
	mux.HandleFunc("POST /api/v1/trips", a.createTripAPI)
	mux.HandleFunc("GET /api/v1/trips", a.listTripsAPI)
	mux.HandleFunc("GET /healthz", a.health)
	mux.Handle("GET /static/", http.StripPrefix("/static/", web.StaticHandler()))
	return securityHeaders(recoverRequests(logRequests(mux)))
}

func (a *app) home(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, a.readLimiter) {
		a.renderError(w, r, http.StatusTooManyRequests, "Too many refreshes", "Please wait a minute and try again.")
		return
	}
	now := a.now()
	start, end := lookbackRange(now, 1, a.location)
	items, err := a.store.List(r.Context(), start, end)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, "Could not load Momo's trips", "Please try again in a moment.")
		return
	}
	data := web.HomePageData{Now: now.In(a.location), RecentTrips: presentTrips(items, a.location)}
	data.TodayTrips = len(items)
	for _, item := range items {
		if item.HasPoo {
			data.TodayPoos++
		}
	}
	switch r.URL.Query().Get("recorded") {
	case "poo":
		data.SuccessMessage = "Pee + poo saved at " + now.In(a.location).Format("3:04 PM") + "."
	case "no-poo":
		data.SuccessMessage = "Pee-only trip saved at " + now.In(a.location).Format("3:04 PM") + "."
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := web.HomePage(data).Render(r.Context(), w); err != nil {
		log.Printf("render home: %v", err)
	}
}

func (a *app) createTripForm(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, a.writeLimiter) {
		a.renderError(w, r, http.StatusTooManyRequests, "Too many check-ins", "The daily trip limit has been reached. Try again tomorrow.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := r.ParseForm(); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "Invalid check-in", "The trip could not be understood.")
		return
	}
	value := r.PostForm.Get("has_poo")
	if len(r.PostForm["has_poo"]) != 1 || (value != "true" && value != "false") {
		a.renderError(w, r, http.StatusBadRequest, "Invalid check-in", "Choose either poo or no poo.")
		return
	}
	hasPoo := value == "true"
	if _, err := a.store.Create(r.Context(), hasPoo); err != nil {
		a.renderError(w, r, http.StatusInternalServerError, "Could not save this trip", "Please try again in a moment.")
		return
	}
	recorded := "no-poo"
	if hasPoo {
		recorded = "poo"
	}
	http.Redirect(w, r, "/?recorded="+recorded, http.StatusSeeOther)
}

func (a *app) history(w http.ResponseWriter, r *http.Request) {
	if !a.allow(w, r, a.readLimiter) {
		a.renderError(w, r, http.StatusTooManyRequests, "Too many refreshes", "Please wait a minute and try again.")
		return
	}
	days, err := parseDays(r, 7)
	if err != nil {
		a.renderError(w, r, http.StatusBadRequest, "Invalid date range", err.Error())
		return
	}
	now := a.now()
	start, end := lookbackRange(now, days, a.location)
	items, err := a.store.List(r.Context(), start, end)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, "Could not load history", "Please try again in a moment.")
		return
	}
	data := web.HistoryPageData{
		Days: days, DaySummaries: summarizeDays(items, now, days, a.location),
		RecentTrips: presentTrips(items, a.location),
	}
	w.Header().Set("Cache-Control", "no-store")
	if err := web.HistoryPage(data).Render(r.Context(), w); err != nil {
		log.Printf("render history: %v", err)
	}
}

func (a *app) createTripAPI(w http.ResponseWriter, r *http.Request) {
	if !a.allowJSON(w, r, a.writeLimiter) {
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(w, http.StatusUnsupportedMediaType, "content_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	var input struct {
		HasPoo *bool `json:"has_poo"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.HasPoo == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", "body must contain a boolean has_poo field")
		return
	}
	if err := ensureJSONEnd(decoder); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_body", "body must contain exactly one JSON object")
		return
	}
	item, err := a.store.Create(r.Context(), *input.HasPoo)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "trip could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, apiTrip(item))
}

func (a *app) listTripsAPI(w http.ResponseWriter, r *http.Request) {
	if !a.allowJSON(w, r, a.readLimiter) {
		return
	}
	days, err := parseDays(r, 1)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_days", err.Error())
		return
	}
	now := a.now()
	start, end := lookbackRange(now, days, a.location)
	items, err := a.store.List(r.Context(), start, end)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "trips could not be loaded")
		return
	}
	responseItems := make([]any, 0, len(items))
	for _, item := range items {
		responseItems = append(responseItems, apiTrip(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days": days, "timezone": a.location.String(),
		"start": start.In(a.location).Format(time.RFC3339),
		"end":   end.In(a.location).Format(time.RFC3339), "trips": responseItems,
	})
}

func (a *app) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := a.store.Ping(ctx); err != nil {
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (a *app) renderError(w http.ResponseWriter, r *http.Request, status int, title, message string) {
	w.WriteHeader(status)
	if err := web.ErrorPage(web.ErrorPageData{StatusCode: status, Title: title, Message: message}).Render(r.Context(), w); err != nil {
		log.Printf("render error page: %v", err)
	}
}

func (a *app) allow(w http.ResponseWriter, r *http.Request, limiter *ratelimit.Limiter) bool {
	now := a.now()
	result := limiter.Allow(clientIP(r), now)
	setRateHeaders(w, result, now)
	return result.Allowed
}

func (a *app) allowJSON(w http.ResponseWriter, r *http.Request, limiter *ratelimit.Limiter) bool {
	if a.allow(w, r, limiter) {
		return true
	}
	writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
	return false
}

func parseDays(r *http.Request, fallback int) (int, error) {
	values, exists := r.URL.Query()["days"]
	if !exists {
		return fallback, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, fmt.Errorf("days must be one integer from 1 to %d", maxLookbackDays)
	}
	for _, char := range values[0] {
		if char < '0' || char > '9' {
			return 0, fmt.Errorf("days must be one integer from 1 to %d", maxLookbackDays)
		}
	}
	days, err := strconv.Atoi(values[0])
	if err != nil || days < 1 || days > maxLookbackDays {
		return 0, fmt.Errorf("days must be one integer from 1 to %d", maxLookbackDays)
	}
	return days, nil
}

func lookbackRange(now time.Time, days int, location *time.Location) (time.Time, time.Time) {
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -(days - 1))
	return start.UTC(), now.UTC()
}

func summarizeDays(items []trips.Trip, now time.Time, days int, location *time.Location) []web.DaySummary {
	local := now.In(location)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	summaries := make([]web.DaySummary, days)
	byDate := make(map[string]int, days)
	for i := range days {
		date := today.AddDate(0, 0, i-(days-1))
		summaries[i].Date = date
		byDate[date.Format("2006-01-02")] = i
	}
	for _, item := range items {
		i, ok := byDate[item.OccurredAt.In(location).Format("2006-01-02")]
		if !ok {
			continue
		}
		summaries[i].Trips++
		if item.HasPoo {
			summaries[i].Poos++
		} else {
			summaries[i].NoPoos++
		}
	}
	return summaries
}

func presentTrips(items []trips.Trip, location *time.Location) []web.Trip {
	result := make([]web.Trip, 0, len(items))
	for _, item := range items {
		result = append(result, web.Trip{ID: item.ID, OccurredAt: item.OccurredAt.In(location), HasPoo: item.HasPoo})
	}
	return result
}

func apiTrip(item trips.Trip) map[string]any {
	return map[string]any{"id": item.ID, "occurred_at": item.OccurredAt.UTC().Format(time.RFC3339Nano), "has_poo": item.HasPoo}
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("trailing JSON value")
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON: %v", err)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func setRateHeaders(w http.ResponseWriter, result ratelimit.Result, now time.Time) {
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(result.Remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(result.Reset.Unix(), 10))
	if !result.Allowed {
		seconds := max(1, int(result.Reset.Sub(now).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; img-src 'self' data:; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func recoverRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Printf("panic serving %s: %v", r.URL.Path, recovered)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s from %s in %s", r.Method, r.URL.RequestURI(), clientIP(r), time.Since(started).Round(time.Millisecond))
	})
}
