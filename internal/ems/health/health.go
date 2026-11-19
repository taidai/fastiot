// Package health resolves communication-link health from polling evidence.
package health

import (
	"time"

	"github.com/simpleiot/simpleiot/data"
)

const (
	Healthy  = "healthy"
	Degraded = "degraded"
	Offline  = "offline"
	Unknown  = "unknown"
)

type Input struct {
	Requests            int
	ConsecutiveFailures int
	OfflineAfter        int
	LastSuccess         time.Time
	PollPeriod          time.Duration
	Timeout             time.Duration
	MaxAge              time.Duration
	CreatedAt           time.Time
}

type Verdict struct {
	State  string
	Reason string
	Age    time.Duration
}

func Evaluate(input Input, now time.Time) Verdict {
	offlineAfter := input.OfflineAfter
	if offlineAfter <= 0 {
		offlineAfter = 3
	}
	maxAge := input.MaxAge
	if maxAge <= 0 {
		maxAge = 5 * time.Second
		if value := 5 * input.PollPeriod; value > maxAge {
			maxAge = value
		}
		if value := 3 * input.Timeout; value > maxAge {
			maxAge = value
		}
	}
	if input.Requests == 0 {
		if !input.CreatedAt.IsZero() && now.Sub(input.CreatedAt) < maxAge {
			return Verdict{State: Unknown, Reason: "awaiting_first_success"}
		}
		return Verdict{State: Unknown, Reason: "no_observation"}
	}
	age := now.Sub(input.LastSuccess)
	if !input.LastSuccess.IsZero() && age < 0 {
		return Verdict{State: Offline, Reason: "invalid_time", Age: age}
	}
	if input.LastSuccess.IsZero() {
		if !input.CreatedAt.IsZero() && now.Sub(input.CreatedAt) < maxAge {
			return Verdict{State: Unknown, Reason: "awaiting_first_success"}
		}
		return Verdict{State: Offline, Reason: "no_success"}
	}
	if input.ConsecutiveFailures >= offlineAfter {
		return Verdict{State: Offline, Reason: "consecutive_failures", Age: age}
	}
	if age > maxAge {
		return Verdict{State: Offline, Reason: "source_expired", Age: age}
	}
	if input.ConsecutiveFailures > 0 {
		return Verdict{State: Degraded, Reason: "consecutive_failures", Age: age}
	}
	return Verdict{State: Healthy, Age: age}
}

// CreatedAt returns the node's creation timestamp, not its latest rename time.
func CreatedAt(points data.Points) time.Time {
	for _, point := range points {
		if point.Type == data.PointTypeCreated {
			return point.Time
		}
	}
	for _, point := range points {
		if point.Type == data.PointTypeDescription {
			return point.Time
		}
	}
	return time.Time{}
}
