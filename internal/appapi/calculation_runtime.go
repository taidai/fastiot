package appapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"fastiot/internal/calculation"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/observation"
	"fastiot/internal/ems/signal"
	"fastiot/internal/ems/state"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

func (h *Handler) CalculationRuntime() *calculation.Runtime { return h.calculationRuntime }

func (h *Handler) invalidatePhysicalCalculations(id string) {
	ids := []string{id}
	for _, result := range h.CalculationRuntime().Snapshot().Results {
		for _, leaf := range result.Leaves {
			if leaf.Source == "observation" && h.users.IsUnder(leaf.ID, id) {
				ids = append(ids, leaf.ID)
			}
		}
	}
	h.CalculationRuntime().Invalidate(ids)
}

func (h *Handler) RunCalculations(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	stop, err := client.SubscribePoints(h.nc, "*", func(points []data.Point) {
		for _, p := range points {
			switch p.Type {
			case calculationDocument, virtualInputDocument, data.PointTypeAddress, data.PointTypeID, data.PointTypeModbusIOType, data.PointTypeDataFormat, data.PointTypeScale, data.PointTypeOffset, data.PointTypeUnits, data.PointTypeDisabled, data.PointTypeProtocol, data.PointTypeURI, data.PointTypePort, data.PointTypeBaud, data.PointTypePollPeriod, data.PointTypeClientServer, "dataBits", "parity", "stopBits":
				notify()
				return
			}
		}
	})
	if err != nil {
		return err
	}
	defer stop()
	var changes <-chan struct{}
	if h.ems != nil && h.ems.Service != nil {
		changes = h.ems.Service.State.Changed()
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-changes:
		case <-timer.C:
		}
		if !h.ready.Load() {
			timer.Reset(100 * time.Millisecond)
			continue
		}
		delay, err := h.runCalculationBatch()
		if err == nil {
			err = h.persistDerivedInputs()
		}
		if err != nil {
			snapshot := h.CalculationRuntime().Snapshot()
			ids := []string{}
			for id := range snapshot.Results {
				ids = append(ids, id)
			}
			h.CalculationRuntime().Invalidate(ids)
			delay = time.Second
		}
		timer.Reset(delay)
	}
}

func (h *Handler) runCalculationBatch() (time.Duration, error) {
	h.virtualInputMu.Lock()
	defer h.virtualInputMu.Unlock()
	catalog, err := h.loadCalculationCatalog()
	if err != nil {
		return time.Second, err
	}
	formulas := map[string]calculation.Formula{}
	for id, d := range catalog.documents {
		if d.Applied != nil {
			formulas[id] = calculation.Formula{Definition: *d.Applied, Version: d.Version}
		}
	}
	type mapping struct {
		device string
		point  channel.Point
	}
	expected := map[string]mapping{}
	ambiguous := map[string]bool{}
	if h.ems != nil && h.ems.Buses != nil {
		configs, err := h.ems.Buses()
		if err != nil {
			return time.Second, err
		}
		for _, cfg := range configs {
			for _, device := range cfg.Devices {
				for _, point := range device.Points {
					old, exists := expected[point.ID]
					if exists && (old.device != device.ID || old.point.ConfigStamp != point.ConfigStamp) {
						ambiguous[point.ID] = true
					}
					expected[point.ID] = mapping{device: device.ID, point: point}
				}
			}
		}
	}
	captured := state.Capture{At: time.Now()}
	maxAge := time.Duration(0)
	if h.ems != nil && h.ems.Service != nil {
		maxAge = h.ems.Service.MaxAge
		captured = h.ems.Service.State.Capture(time.Now, maxAge)
	}
	current := map[state.Key]state.Value{}
	for _, v := range captured.Values {
		current[state.Key{DeviceID: v.DeviceID, PointID: v.PointID}] = v
	}
	clock := h.inputClock(captured.At)
	snapshot := calculation.Snapshot{At: captured.At, Inputs: map[string]calculation.Sample{}}
	delay := time.Second
	for id, metadata := range catalog.sources {
		node := catalog.nodes[id]
		sample := calculation.Sample{ID: id, Kind: metadata.Kind, Unit: metadata.Unit, Quality: "uninitialized", Reason: "no_observation"}
		switch node.Type {
		case data.NodeTypeModbusIO:
			sample.Source = "observation"
			mapping, ok := expected[id]
			if !ok {
				sample.Reason = "source_not_polling"
				break
			}
			if ambiguous[id] {
				sample.Quality = "invalid"
				sample.Reason = "ambiguous_source"
				break
			}
			value, ok := current[state.Key{DeviceID: mapping.device, PointID: id}]
			if !ok {
				break
			}
			sample.Unit = value.Unit
			sample.Version = value.ConfigStamp
			sample.SourceTime = value.SourceTime
			sample.ReceivedTime = value.ReceivedTime
			sample.TimeSource = value.TimeSource
			sample.Quality = value.Quality.String()
			sample.Reason = value.Reason
			sample.ValidUntil = value.ExpiresAt
			if value.ConfigStamp == "" || value.ConfigStamp != mapping.point.ConfigStamp || metadata.Unit != value.Unit {
				sample.Quality = "invalid"
				sample.Reason = "waiting_for_matching_observation"
				break
			}
			if sample.Quality == "good" && value.Value.Kind != signal.Number {
				sample.Quality = "invalid"
				sample.Reason = "input_type"
				break
			}
			if sample.Quality == "good" && value.Value.Valid() {
				number := value.Value.Num
				sample.Value = &number
			}
		case virtualInputType:
			d, err := decodeVirtualInput(node)
			if err != nil {
				sample.Quality = "invalid"
				sample.Reason = "invalid_input_document"
				break
			}
			value := d.Evaluate(clock)
			quality := signal.Uninitialized
			switch value.Quality {
			case "good":
				quality = signal.Good
			case "stale":
				quality = signal.Stale
			case "bad":
				quality = signal.Invalid
			}
			var enteredAt, expiresAt time.Time
			if d.Current != nil {
				enteredAt, expiresAt = d.Current.EnteredAt, d.Current.ExpiresAt
			}
			verdict := observation.Evaluate(observation.Input{
				Value: signal.Num(0), Quality: quality, Reason: value.Reason,
				SourceTime: enteredAt, ReceivedTime: enteredAt, ExpiresAt: expiresAt,
				AllowNoExpiry: d.Source == "configuration",
			}, captured.At)
			sample.Source = d.Source
			sample.Quality = verdict.Quality.String()
			sample.Reason = verdict.Reason
			encoded, _ := json.Marshal(d.Current)
			sample.Version = fmt.Sprintf("%x", sha256.Sum256(encoded))
			if d.Current != nil {
				sample.ChangedAt = d.Current.EnteredAt
				sample.ValidUntil = verdict.ExpiresAt
				if d.Source != "configuration" && sample.Quality == "good" {
					remaining := d.Current.Deadline - clock.Monotonic
					if remaining > 0 && remaining < delay {
						delay = remaining
					}
				}
			}
			if sample.Quality == "good" && metadata.Kind == "number" {
				var number float64
				if json.Unmarshal(value.Value, &number) == nil {
					sample.Value = &number
				} else {
					sample.Quality = "invalid"
					sample.Reason = "input_type"
				}
			}
		}
		if sample.Quality == "good" && !sample.ValidUntil.IsZero() {
			remaining := sample.ValidUntil.Sub(captured.At)
			if remaining > 0 && remaining < delay {
				delay = remaining
			}
		}
		snapshot.Inputs[id] = sample
	}
	h.CalculationRuntime().Publish(formulas, snapshot)
	return delay, nil
}
