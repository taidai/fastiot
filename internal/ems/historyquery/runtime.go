package historyquery

import (
	"errors"
	"time"

	"fastiot/internal/ems/history"
)

// DevicePoint maps one runtime point to its device and declared sample
// interval. HistoryQuery uses it to select the runtime source.
type DevicePoint struct {
	PointID    string
	DeviceID   string
	IntervalMS int
}

// StoreRuntime is the runtime history adapter over the in-memory store.
type StoreRuntime struct {
	Store  *history.Store
	Points func() ([]DevicePoint, error)
}

// Locate reports the runtime device of a point, rejecting a point claimed by
// two devices.
func (r *StoreRuntime) Locate(pointID string) (string, int, bool, error) {
	if r == nil || r.Store == nil || r.Points == nil {
		return "", 0, false, ErrUnavailable
	}
	points, err := r.Points()
	if err != nil {
		return "", 0, false, err
	}
	deviceID, intervalMS := "", 0
	for _, point := range points {
		if point.PointID != pointID {
			continue
		}
		if deviceID != "" && deviceID != point.DeviceID {
			return "", 0, false, errors.New("ambiguous_history_source")
		}
		deviceID, intervalMS = point.DeviceID, point.IntervalMS
	}
	return deviceID, intervalMS, deviceID != "", nil
}

// Timeline returns the runtime observation and quality-event sequence.
func (r *StoreRuntime) Timeline(deviceID, pointID string, start, end time.Time) []history.Sample {
	return r.Store.Timeline(deviceID, pointID, start, end, 0)
}
