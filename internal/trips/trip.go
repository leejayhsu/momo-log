package trips

import "time"

// Trip records one bathroom trip. OccurredAt is assigned by the server.
type Trip struct {
	ID         int64
	OccurredAt time.Time
	HasPoo     bool
}
