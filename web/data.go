package web

import "time"

// Trip is the presentation model shared by the home and history pages.
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

// DaySummary contains local-day totals for one row of the history chart.
type DaySummary struct {
	Date   time.Time
	Trips  int
	Poos   int
	NoPoos int
}

// HistoryPageData contains the selected chart range and its recent events.
type HistoryPageData struct {
	Days         int
	DaySummaries []DaySummary
	RecentTrips  []Trip
}

// ChartMax returns a non-zero scale for comparing both trip types.
func (d HistoryPageData) ChartMax() int {
	maxCount := 1
	for _, day := range d.DaySummaries {
		if day.Poos > maxCount {
			maxCount = day.Poos
		}
		if day.NoPoos > maxCount {
			maxCount = day.NoPoos
		}
	}
	return maxCount
}

// ErrorPageData is safe to show for expected and unexpected request errors.
type ErrorPageData struct {
	StatusCode int
	Title      string
	Message    string
}
