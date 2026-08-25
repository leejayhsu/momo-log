package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"momo-poo/internal/ratelimit"
	"momo-poo/internal/store"
	"momo-poo/internal/trips"
)

type fakeStore struct {
	items        []trips.Trip
	createdPoo   []bool
	deletedIDs   []int64
	deleteResult bool
	listStart    time.Time
	listEnd      time.Time
	err          error
}

type fakePush struct {
	subscriptions []store.PushSubscription
	unsubscribed  []string
	notified      []trips.Trip
	err           error
}

func (p *fakePush) PublicKey() string { return "public-key" }
func (p *fakePush) Subscribe(_ context.Context, subscription store.PushSubscription) error {
	p.subscriptions = append(p.subscriptions, subscription)
	return p.err
}
func (p *fakePush) Unsubscribe(_ context.Context, endpoint string) error {
	p.unsubscribed = append(p.unsubscribed, endpoint)
	return p.err
}
func (p *fakePush) NotifyTrip(_ context.Context, trip trips.Trip, _ *time.Location) error {
	p.notified = append(p.notified, trip)
	return p.err
}

func (s *fakeStore) Create(_ context.Context, hasPoo bool) (trips.Trip, error) {
	if s.err != nil {
		return trips.Trip{}, s.err
	}
	s.createdPoo = append(s.createdPoo, hasPoo)
	return trips.Trip{ID: int64(len(s.createdPoo)), OccurredAt: time.Date(2026, 8, 25, 18, 30, 0, 0, time.UTC), HasPoo: hasPoo}, nil
}

func (s *fakeStore) List(_ context.Context, start, end time.Time) ([]trips.Trip, error) {
	s.listStart, s.listEnd = start, end
	return s.items, s.err
}

func (s *fakeStore) Delete(_ context.Context, id int64) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	s.deletedIDs = append(s.deletedIDs, id)
	return s.deleteResult, nil
}

func (s *fakeStore) Ping(context.Context) error { return s.err }

func testApp(store tripStore, loc *time.Location, now time.Time) *app {
	a := newApp(store, loc, ratelimit.NewWrite(50, loc, 100), ratelimit.NewRead(60, 100))
	a.now = func() time.Time { return now }
	return a
}

func TestCreateTripAPI(t *testing.T) {
	loc := time.FixedZone("test", -7*60*60)
	store := &fakeStore{}
	handler := testApp(store, loc, time.Date(2026, 8, 25, 12, 0, 0, 0, loc)).routes()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(`{"has_poo":true}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, req)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(store.createdPoo) != 1 || !store.createdPoo[0] {
		t.Fatalf("created values = %v", store.createdPoo)
	}
	var body struct {
		HasPoo bool `json:"has_poo"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.HasPoo {
		t.Fatalf("response body = %s, error = %v", response.Body.String(), err)
	}
}

func TestCreateTripNotifiesWithoutFailingOnPushError(t *testing.T) {
	store := &fakeStore{}
	push := &fakePush{err: errors.New("push unavailable")}
	a := testApp(store, time.UTC, time.Now())
	a.push = push
	request := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(`{"has_poo":false}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	a.routes().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if len(push.notified) != 1 || push.notified[0].HasPoo {
		t.Fatalf("notified trips = %+v", push.notified)
	}
}

func TestPushSubscriptionAPI(t *testing.T) {
	push := &fakePush{}
	a := testApp(&fakeStore{}, time.UTC, time.Now())
	a.push = push
	handler := a.routes()
	p256dh := base64.RawURLEncoding.EncodeToString(make([]byte, 65))
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	body := `{"endpoint":"https://push.example/subscription","keys":{"p256dh":"` + p256dh + `","auth":"` + auth + `"}}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/push-subscriptions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || len(push.subscriptions) != 1 {
		t.Fatalf("status = %d, subscriptions = %+v, body = %s", response.Code, push.subscriptions, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/v1/push-subscriptions", strings.NewReader(`{"endpoint":"https://push.example/subscription"}`))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(push.unsubscribed) != 1 {
		t.Fatalf("status = %d, unsubscribed = %+v, body = %s", response.Code, push.unsubscribed, response.Body.String())
	}
}

func TestPushSubscriptionAPIRejectsInvalidSubscription(t *testing.T) {
	push := &fakePush{}
	a := testApp(&fakeStore{}, time.UTC, time.Now())
	a.push = push
	request := httptest.NewRequest(http.MethodPost, "/api/v1/push-subscriptions", strings.NewReader(`{"endpoint":"http://push.example/subscription","keys":{"p256dh":"bad","auth":"bad"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	a.routes().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || len(push.subscriptions) != 0 {
		t.Fatalf("status = %d, subscriptions = %+v", response.Code, push.subscriptions)
	}
}

func TestCreateTripAPIRejectsInvalidBodies(t *testing.T) {
	loc := time.UTC
	tests := []struct {
		name        string
		contentType string
		body        string
		status      int
	}{
		{"missing field", "application/json", `{}`, http.StatusBadRequest},
		{"wrong type", "application/json", `{"has_poo":"yes"}`, http.StatusBadRequest},
		{"unknown field", "application/json", `{"has_poo":true,"note":"x"}`, http.StatusBadRequest},
		{"trailing value", "application/json", `{"has_poo":true} {}`, http.StatusBadRequest},
		{"wrong content type", "text/plain", `{"has_poo":true}`, http.StatusUnsupportedMediaType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			handler := testApp(store, loc, time.Now()).routes()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tt.status {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.status, response.Body.String())
			}
			if len(store.createdPoo) != 0 {
				t.Fatalf("unexpected creates = %v", store.createdPoo)
			}
		})
	}
}

func TestListTripsAPIDaysUseLocalCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 10, 15, 0, 0, loc)
	store := &fakeStore{items: []trips.Trip{{ID: 1, OccurredAt: now.UTC(), HasPoo: false}}}
	handler := testApp(store, loc, now).routes()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/trips?days=2", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	wantStart := time.Date(2026, 3, 8, 0, 0, 0, 0, loc).UTC()
	if !store.listStart.Equal(wantStart) || !store.listEnd.Equal(now.UTC()) {
		t.Fatalf("range = [%s, %s), want [%s, %s)", store.listStart, store.listEnd, wantStart, now.UTC())
	}
}

func TestInvalidDays(t *testing.T) {
	for _, query := range []string{"days=0", "days=-1", "days=", "days=1&days=2", "days=367", "days=1.5"} {
		t.Run(query, func(t *testing.T) {
			response := httptest.NewRecorder()
			testApp(&fakeStore{}, time.UTC, time.Now()).routes().ServeHTTP(response,
				httptest.NewRequest(http.MethodGet, "/api/v1/trips?"+query, nil))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", response.Code)
			}
		})
	}
}

func TestCreateTripFormRedirects(t *testing.T) {
	store := &fakeStore{}
	handler := testApp(store, time.UTC, time.Now()).routes()
	req := httptest.NewRequest(http.MethodPost, "/trips", strings.NewReader("has_poo=false"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/?recorded=no-poo" {
		t.Fatalf("status = %d, location = %q", response.Code, response.Header().Get("Location"))
	}
	if len(store.createdPoo) != 1 || store.createdPoo[0] {
		t.Fatalf("created values = %v", store.createdPoo)
	}
}

func TestDeleteTripForm(t *testing.T) {
	store := &fakeStore{deleteResult: true}
	handler := testApp(store, time.UTC, time.Now()).routes()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/trips/42/delete?days=30", nil))

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/history?days=30" {
		t.Fatalf("status = %d, location = %q", response.Code, response.Header().Get("Location"))
	}
	if len(store.deletedIDs) != 1 || store.deletedIDs[0] != 42 {
		t.Fatalf("deleted IDs = %v, want [42]", store.deletedIDs)
	}
}

func TestDeleteTripAPI(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		store       *fakeStore
		wantStatus  int
		wantDeleted []int64
		wantCode    string
	}{
		{name: "deleted", path: "/api/v1/trips/42", store: &fakeStore{deleteResult: true}, wantStatus: http.StatusNoContent, wantDeleted: []int64{42}},
		{name: "not found", path: "/api/v1/trips/42", store: &fakeStore{}, wantStatus: http.StatusNotFound, wantDeleted: []int64{42}, wantCode: "not_found"},
		{name: "invalid ID", path: "/api/v1/trips/nope", store: &fakeStore{}, wantStatus: http.StatusBadRequest, wantCode: "invalid_id"},
		{name: "store error", path: "/api/v1/trips/42", store: &fakeStore{err: errors.New("database down")}, wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			testApp(tt.store, time.UTC, time.Now()).routes().ServeHTTP(response, httptest.NewRequest(http.MethodDelete, tt.path, nil))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if len(tt.store.deletedIDs) != len(tt.wantDeleted) {
				t.Fatalf("deleted IDs = %v, want %v", tt.store.deletedIDs, tt.wantDeleted)
			}
			for i, id := range tt.wantDeleted {
				if tt.store.deletedIDs[i] != id {
					t.Fatalf("deleted IDs = %v, want %v", tt.store.deletedIDs, tt.wantDeleted)
				}
			}
			if tt.wantCode != "" && !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s, want error code %q", response.Body.String(), tt.wantCode)
			}
		})
	}
}

func TestRateLimitAndHealth(t *testing.T) {
	loc := time.UTC
	store := &fakeStore{}
	a := newApp(store, loc, ratelimit.NewWrite(1, loc, 10), ratelimit.NewRead(1, 10))
	a.now = func() time.Time { return time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC) }
	handler := a.routes()
	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/trips", nil))
		if response.Code != want {
			t.Fatalf("request %d status = %d, want %d", i, response.Code, want)
		}
	}

	store.err = errors.New("database down")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d", response.Code)
	}
}

func TestUnknownGETReturnsNotFound(t *testing.T) {
	response := httptest.NewRecorder()
	testApp(&fakeStore{}, time.UTC, time.Now()).routes().ServeHTTP(response,
		httptest.NewRequest(http.MethodGet, "/not-a-page", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}
