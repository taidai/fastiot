// Package state keeps the latest value, quality and time of every point, so
// the rule engine, allocator, command manager and UI read one consistent view.
package state

import (
	"sort"
	"sync"
	"time"

	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/signal"
)

// Key identifies one point on one device.
type Key struct {
	DeviceID string
	PointID  string
}

// Value is the latest accepted reading of one point.
type Value struct {
	DeviceID        string
	PointID         string
	Value           signal.Value
	Quality         signal.Quality
	SourceTime      time.Time
	ReceivedTime    time.Time
	TimeSource      string
	Unit            string
	ConfigStamp     string
	Reason          string
	LastAttemptTime time.Time
}

// Store is safe for concurrent readers and one writer.
type Store struct {
	mu       sync.RWMutex
	values   map[Key]Value
	health   map[string]channel.Health
	updated  map[string]time.Time
	changed  chan struct{}
	revision uint64
}

// New returns an empty store.
func New() *Store {
	return &Store{
		values:  make(map[Key]Value),
		health:  make(map[string]channel.Health),
		updated: make(map[string]time.Time),
		changed: make(chan struct{}, 1),
	}
}

// Apply records newer observations and returns only newly accepted readings.
func (s *Store) Apply(now time.Time, results []channel.Result) []channel.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	accepted := make([]channel.Result, 0, len(results))
	for _, result := range results {
		key := Key{result.DeviceID, result.PointID}
		previous, exists := s.values[key]
		if exists && !result.SourceTime.IsZero() && result.SourceTime.UTC().Before(previous.SourceTime.UTC()) {
			continue
		}
		reason := ""
		switch {
		case result.Error != "":
			reason = result.Error
		case result.Quality != signal.Good:
			reason = "quality_" + result.Quality.String()
		case !result.Value.Valid():
			reason = "invalid_value"
		case result.SourceTime.IsZero():
			reason = "missing_source_time"
		case result.SourceTime.UTC().After(now.UTC()):
			reason = "future_source_time"
		case exists && result.SourceTime.Equal(previous.SourceTime) && result.Value != previous.Value:
			reason = "conflicting_source_time"
		}
		if reason != "" {
			previous.DeviceID, previous.PointID = result.DeviceID, result.PointID
			previous.Quality, previous.Reason = signal.Invalid, reason
			previous.LastAttemptTime = now
			s.values[key] = previous
			s.notifyLocked()
			continue
		}
		if exists && !result.SourceTime.After(previous.SourceTime) {
			continue
		}
		s.values[key] = Value{
			DeviceID: result.DeviceID, PointID: result.PointID,
			Value: result.Value, Quality: result.Quality,
			SourceTime: result.SourceTime, ReceivedTime: now, LastAttemptTime: now,
			TimeSource: result.TimeSource, Unit: result.Unit, ConfigStamp: result.ConfigStamp,
		}
		s.notifyLocked()
		accepted = append(accepted, result)
	}
	return accepted
}

// SetHealth records the communication health of one device.
func (s *Store) SetHealth(deviceID string, health channel.Health, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.health[deviceID] = health
	s.updated[deviceID] = now
}

// Health returns the last recorded health, or unknown.
func (s *Store) Health(deviceID string) channel.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if health, ok := s.health[deviceID]; ok {
		return health
	}
	return channel.HealthUnknown
}

// Get returns the point's own quality, downgraded when its observation expires.
// Aggregate device health is diagnostic and does not override successful points.
func (s *Store) Get(now time.Time, deviceID, pointID string, maxAge time.Duration) (Value, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[Key{deviceID, pointID}]
	if !ok {
		return Value{}, false
	}
	return s.effective(value, now, maxAge), true
}

// Samples returns every point of a device keyed by point id, ready for the
// rule engine and safety layer.
func (s *Store) Samples(deviceID string, now time.Time, maxAge time.Duration) map[string]signal.Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	samples := make(map[string]signal.Sample)
	for key, value := range s.values {
		if key.DeviceID != deviceID {
			continue
		}
		value = s.effective(value, now, maxAge)
		samples[key.PointID] = signal.Sample{
			Input: key.PointID, Value: value.Value,
			Quality:    value.Quality,
			SourceTime: value.SourceTime, ReceivedTime: value.ReceivedTime,
		}
	}
	return samples
}

type Capture struct {
	At       time.Time
	Revision uint64
	Values   []Value
}

func (s *Store) notifyLocked() {
	s.revision++
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Changed coalesces wakeups for one consumer; it is not an observation log.
func (s *Store) Changed() <-chan struct{} { return s.changed }
func (s *Store) Capture(clock func() time.Time, maxAge time.Duration) Capture {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := clock()
	return Capture{At: now, Revision: s.revision, Values: s.snapshotLocked(now, maxAge)}
}

// Snapshot returns all values sorted by device and point.
func (s *Store) Snapshot(now time.Time, maxAge time.Duration) []Value {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked(now, maxAge)
}

func (s *Store) snapshotLocked(now time.Time, maxAge time.Duration) []Value {
	values := make([]Value, 0, len(s.values))
	for _, value := range s.values {
		values = append(values, s.effective(value, now, maxAge))
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].DeviceID != values[j].DeviceID {
			return values[i].DeviceID < values[j].DeviceID
		}
		return values[i].PointID < values[j].PointID
	})
	return values
}

func (s *Store) effective(value Value, now time.Time, maxAge time.Duration) Value {
	if value.Quality != signal.Good {
		return value
	}
	if value.SourceTime.UTC().After(now.UTC()) {
		value.Quality, value.Reason = signal.Invalid, "future_source_time"
	} else if maxAge > 0 && now.Sub(value.SourceTime) > maxAge {
		value.Quality, value.Reason = signal.Stale, "source_expired"
	}
	return value
}
