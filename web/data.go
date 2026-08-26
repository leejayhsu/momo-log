package web

import "time"

// Trip is the presentation model for a bathroom trip.
type Trip struct {
	ID         int64
	OccurredAt time.Time
	HasPoo     bool
}

// HomePageData contains everything needed to render the trip logger.
type HomePageData struct {
	Now            time.Time
	TodayTrips     int
	TodayPoos      int
	RecentTrips    []Trip
	SuccessMessage string
}

// TodayNoPoos returns the number of today's trips that did not include a poo.
func (d HomePageData) TodayNoPoos() int {
	count := d.TodayTrips - d.TodayPoos
	if count < 0 {
		return 0
	}
	return count
}

// ErrorPageData is safe to show for expected and unexpected request errors.
type ErrorPageData struct {
	StatusCode int
	Title      string
	Message    string
}
