package genscreen

import (
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// Write outcomes. A preview write is always simulated or rejected; there is
// no path from here to a device.
const (
	WriteSimulated = "simulated"
	WriteRejected  = "rejected"

	ReasonReadOnly      = "point_read_only"
	ReasonNotInPreview  = "point_not_in_preview"
	ReasonNotNumeric    = "point_not_numeric"
	ReasonInvalidNumber = "invalid_number"

	QualityGood = "good"
)

// Value is one simulated reading.
type Value struct {
	Value     any       `json:"value"`
	Time      time.Time `json:"time"`
	Quality   string    `json:"quality"`
	Simulated bool      `json:"simulated"`
}

// WriteEntry is one recorded preview write, shown to the deliverer.
type WriteEntry struct {
	At     time.Time `json:"at"`
	Point  string    `json:"point"`
	Value  float64   `json:"value"`
	Status string    `json:"status"`
}

// WriteOutcome is what a write binding gets back.
type WriteOutcome struct {
	Status string     `json:"status"`
	Reason string     `json:"reason,omitempty"`
	Value  Value      `json:"value"`
	Entry  WriteEntry `json:"entry"`
}

// Simulator stands in for the device during preview. Readings are a stable
// function of the point ID and the clock, and writes change nothing outside
// this process.
type Simulator struct {
	mu     sync.Mutex
	Now    func() time.Time
	points map[string]*simPoint
	order  []string
	log    []WriteEntry
}

type simPoint struct {
	point  Point
	number float64
	text   string
	byHand bool
}

// NewSimulator builds one preview device for the catalog points.
func NewSimulator(points []Point) *Simulator {
	simulator := &Simulator{Now: time.Now, points: map[string]*simPoint{}}
	for _, point := range points {
		entry := &simPoint{point: point, number: baseNumber(point.ID), text: "模拟文本"}
		simulator.points[point.ID] = entry
		simulator.order = append(simulator.order, point.ID)
	}
	return simulator
}

func baseNumber(id string) float64 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(id))
	seed := hasher.Sum32()
	return float64(20+seed%50) + float64(seed%100)/100
}

// Read returns the simulated readings for the requested IDs that belong to
// this preview.
func (s *Simulator) Read(ids []string) map[string]Value {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	values := map[string]Value{}
	for _, id := range ids {
		entry, ok := s.points[id]
		if !ok {
			continue
		}
		values[id] = entry.valueLocked(now)
	}
	return values
}

func (p *simPoint) valueLocked(now time.Time) Value {
	if p.point.Kind == "text" {
		return Value{Value: p.text, Time: now, Quality: QualityGood, Simulated: true}
	}
	number := p.number
	if !p.byHand {
		hasher := fnv.New32a()
		_, _ = hasher.Write([]byte(p.point.ID))
		phase := float64(hasher.Sum32()%360) * math.Pi / 180
		number += 8 * math.Sin(float64(now.Unix())/15+phase)
	}
	return Value{Value: math.Round(number*100) / 100, Time: now, Quality: QualityGood, Simulated: true}
}

// Write applies a value to the preview device only, and records it.
func (s *Simulator) Write(id string, value float64) WriteOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.points[id]
	if !ok {
		return WriteOutcome{Status: WriteRejected, Reason: ReasonNotInPreview}
	}
	if !entry.point.Writable {
		return WriteOutcome{Status: WriteRejected, Reason: ReasonReadOnly}
	}
	if entry.point.Kind == "text" {
		return WriteOutcome{Status: WriteRejected, Reason: ReasonNotNumeric}
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return WriteOutcome{Status: WriteRejected, Reason: ReasonInvalidNumber}
	}
	entry.byHand = true
	entry.number = value
	now := s.Now()
	outcome := WriteOutcome{
		Status: WriteSimulated,
		Value:  Value{Value: value, Time: now, Quality: QualityGood, Simulated: true},
		Entry:  WriteEntry{At: now, Point: id, Value: value, Status: WriteSimulated},
	}
	s.log = append(s.log, outcome.Entry)
	if len(s.log) > 50 {
		s.log = s.log[len(s.log)-50:]
	}
	return outcome
}

// Log returns the most recent simulated writes, oldest first.
func (s *Simulator) Log() []WriteEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WriteEntry{}, s.log...)
}
