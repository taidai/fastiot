package dailystats

import (
	"errors"
	"time"
)

// Config belongs to the layer, not to any removable screen block. Each metric
// has one explicit source; summing unnamed devices would double-count meters.
type Config struct {
	Version              int               `json:"version"`
	TimeZone             string            `json:"time_zone"`
	Energy               *EnergyBinding    `json:"energy,omitempty"`
	Runtime              *RuntimeBinding   `json:"runtime,omitempty"`
	Freshness            *FreshnessBinding `json:"freshness,omitempty"`
	CommunicationPointID string            `json:"communication_point_id,omitempty"`
}

func (c Config) PointIDs() []string {
	ids := []string{}
	if c.Energy != nil {
		ids = append(ids, c.Energy.PointID)
	}
	if c.Runtime != nil {
		ids = append(ids, c.Runtime.PointID)
	}
	if c.Freshness != nil {
		ids = append(ids, c.Freshness.PointID)
	}
	if c.CommunicationPointID != "" {
		ids = append(ids, c.CommunicationPointID)
	}
	return ids
}
func (c Config) Validate() error {
	if c.Version < 0 {
		return errors.New("invalid_version")
	}
	if _, err := Day("", c.TimeZone, time.Now()); err != nil {
		return err
	}
	bounded := func(v float64) bool { return finite(v) && v > 0 && v <= 86400 }
	if b := c.Energy; b != nil && (b.PointID == "" || b.Unit != "kWh" || !bounded(b.MaxGapSeconds) || b.ResetPolicy != "discard_interval") {
		return errors.New("unsupported_energy_semantics")
	}
	if b := c.Runtime; b != nil && (b.PointID == "" || !bounded(b.MaxGapSeconds) || b.RunningValue == nil || b.StoppedValue == nil || !finite(*b.RunningValue) || !finite(*b.StoppedValue) || *b.RunningValue == *b.StoppedValue) {
		return errors.New("unsupported_runtime_semantics")
	}
	if b := c.Freshness; b != nil && (b.PointID == "" || !bounded(b.MaxAgeSeconds)) {
		return errors.New("unsupported_freshness_semantics")
	}
	return nil
}
