package alarmwatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/alarmtype"
	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/health"
	"fastiot/internal/ems/signal"
	"fastiot/internal/ems/state"
)

type Watcher struct {
	NC                 *nats.Conn
	Alarms             *alarm.Engine
	Interval           time.Duration
	State              *state.Store
	MaxAge             time.Duration
	Buses              func() ([]bridge.BusConfig, error)
	mu                 sync.Mutex
	generation         uint64
	changing           int
	configurationReady bool
	raised             map[string]bool
	pending            map[string]time.Time
	configs            map[string]typeEntry
	views              map[string]NodeView
	mappings           map[string]physicalMapping
	lastRaised         map[string]time.Time
}

type typeEntry struct {
	root        string
	config      alarmtype.Config
	fingerprint string
}
type physicalMapping struct {
	device, stamp string
	kind          signal.Kind
}

func NewWatcher(nc *nats.Conn, alarms *alarm.Engine) *Watcher {
	return &Watcher{NC: nc, Alarms: alarms, Interval: 2 * time.Second, MaxAge: 15 * time.Second, raised: map[string]bool{}, pending: map[string]time.Time{}, configs: map[string]typeEntry{}, views: map[string]NodeView{}, mappings: map[string]physicalMapping{}, lastRaised: map[string]time.Time{}}
}

func (w *Watcher) Run(ctx context.Context) error {
	interval := w.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := w.Scan(time.Now()); err != nil {
		log.Println("alarm watch: initial scan:", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			if err := w.Scan(now); err != nil {
				log.Println("alarm watch: scan:", err)
			}
		}
	}
}

func (w *Watcher) BeginConfiguration() func() {
	w.mu.Lock()
	w.generation++
	w.changing++
	w.configurationReady = false
	for key := range w.configs {
		w.Alarms.MarkUnknown(time.Now(), key, "configuration_pending")
		delete(w.lastRaised, key)
	}
	w.mu.Unlock()
	return func() { w.mu.Lock(); w.changing--; w.generation++; w.mu.Unlock() }
}

func (w *Watcher) Scan(now time.Time) error {
	w.mu.Lock()
	generation, changing := w.generation, w.changing
	w.mu.Unlock()
	if changing > 0 {
		return nil
	}
	nodes := []data.NodeEdge{}
	for _, typ := range []string{"decisionTable", "modbus", "variable", "modbusIo", "device", "group"} {
		list, err := client.GetNodes(w.NC, "all", "all", typ, false)
		if err != nil {
			return err
		}
		nodes = append(nodes, list...)
	}
	views := []NodeView{}
	byID := map[string]NodeView{}
	configs := map[string]typeEntry{}
	for _, node := range nodes {
		view := NodeView{ID: node.ID, Type: node.Type, Text: map[string]string{}, Num: map[string]float64{}}
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			view.Text[point.Type] = point.Txt()
			if value, err := point.ValueFloat(); err == nil {
				view.Num[point.Type] = value
				if point.Type == data.PointTypeValue {
					view.Value = value
					view.HasValue = true
					view.ValueTime = point.Time
				}
			}
			if point.Type == data.PointTypeDescription || point.Type == data.PointTypeCreated {
				view.Created = point.Time
			}
			if point.Type == "easyemsAlarmType" {
				var config alarmtype.Config
				if json.Unmarshal([]byte(point.Txt()), &config) == nil && config.Validate() == nil {
					key := "alarm-type:" + node.ID + ":" + config.Code + ":" + config.PointID
					configs[key] = typeEntry{root: node.ID, config: config, fingerprint: point.Txt()}
				}
			}
		}
		if node.Type == data.NodeTypeModbus {
			view.Offline = channelOffline(view, now)
		}
		views = append(views, view)
		byID[node.ID] = view
	}
	mappings := map[string]physicalMapping{}
	if w.Buses != nil {
		buses, err := w.Buses()
		if err != nil {
			return err
		}
		for _, bus := range buses {
			for _, device := range bus.Devices {
				for _, point := range device.Points {
					mapping := physicalMapping{device.ID, point.ConfigStamp, point.Kind}
					if previous, exists := mappings[point.ID]; exists && previous != mapping {
						mapping = physicalMapping{}
					}
					mappings[point.ID] = mapping
				}
			}
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if generation != w.generation || w.changing > 0 {
		return nil
	}
	active := map[string]bool{}
	for _, finding := range Evaluate(views, now) {
		active[finding.Key] = true
		_, _ = w.Alarms.Raise(now, finding.Key, finding.Severity, finding.Title, finding.Detail, finding.DeviceID, "")
	}
	for key := range w.raised {
		if !active[key] {
			_, _ = w.Alarms.Clear(now, key)
		}
	}
	w.raised = active
	for key := range w.configs {
		if _, ok := configs[key]; !ok {
			w.Alarms.Retire(now, key, "definition_removed")
			delete(w.pending, key)
			delete(w.lastRaised, key)
		}
	}
	for key, next := range configs {
		if previous, ok := w.configs[key]; !ok || previous.fingerprint != next.fingerprint {
			delete(w.pending, key)
			delete(w.lastRaised, key)
		}
	}
	w.configs, w.views, w.mappings = configs, byID, mappings
	w.configurationReady = true
	snapshot := state.Capture{At: now}
	if w.State != nil {
		snapshot = w.State.Capture(func() time.Time { return now }, w.MaxAge)
	}
	w.evaluateTypes(snapshot, false)
	return nil
}

func (w *Watcher) Observe(snapshot state.Capture) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.configurationReady {
		return
	}
	w.evaluateTypes(snapshot, true)
}

func (w *Watcher) evaluateTypes(snapshot state.Capture, physicalOnly bool) {
	now := snapshot.At
	values := map[state.Key]state.Value{}
	for _, value := range snapshot.Values {
		values[state.Key{DeviceID: value.DeviceID, PointID: value.PointID}] = value
	}
	for key, entry := range w.configs {
		config := entry.config
		source, found := w.views[config.PointID]
		physical := found && source.Type == data.NodeTypeModbusIO
		if physicalOnly && !physical {
			continue
		}
		if !config.Enabled {
			delete(w.pending, key)
			w.Alarms.Retire(now, key, "definition_disabled")
			continue
		}
		sample := alarmtype.Sample{}
		var evidence *alarm.InputEvidence
		deviceID, pointID := config.PointID, config.PointID
		if physical {
			mapping := w.mappings[config.PointID]
			deviceID = mapping.device
			value, ok := values[state.Key{DeviceID: mapping.device, PointID: config.PointID}]
			if ok && mapping.stamp != "" && value.ConfigStamp == mapping.stamp && value.Quality == signal.Good && value.Value.Kind == mapping.kind {
				switch value.Value.Kind {
				case signal.Number:
					sample = alarmtype.Sample{Present: true, Value: value.Value.Num, ValueTime: value.SourceTime}
				case signal.Bool:
					sample = alarmtype.Sample{Present: true, Value: data.BoolToFloat(value.Value.Bool), ValueTime: value.SourceTime}
				}
				if sample.Present {
					kind := "number"
					if value.Value.Kind == signal.Bool {
						kind = "bool"
					}
					evidence = &alarm.InputEvidence{PointID: config.PointID, Value: sample.Value, Kind: kind, Unit: value.Unit, Quality: "good", Source: "observation", SourceTime: value.SourceTime, ReceivedTime: value.ReceivedTime, ConfigStamp: value.ConfigStamp}
				}
			}
		} else if found {
			sample = alarmtype.Sample{Present: source.HasValue, Offline: source.Offline, Value: source.Value, ValueTime: source.ValueTime}
			if source.Type == data.NodeTypeModbus {
				pointID = ""
				sample.Present = source.Num["lastGoodPoll"] > 0
			} else if sample.Present {
				evidence = &alarm.InputEvidence{PointID: config.PointID, Value: sample.Value, Kind: "number", Unit: source.Text["units"], Quality: "good", Source: "legacy_variable", SourceTime: source.ValueTime}
			}
		}
		condition := config.Evaluate(now, sample)
		if condition == alarmtype.Unknown {
			delete(w.pending, key)
			w.Alarms.MarkUnknown(now, key, "input_unavailable")
			continue
		}
		if condition == alarmtype.Normal {
			delete(w.pending, key)
			delete(w.lastRaised, key)
			w.clearType(now, key, config.RecordRecovery, evidence)
			continue
		}
		first, seen := w.pending[key]
		if !seen {
			w.pending[key] = now
			first = now
		}
		if !w.Alarms.ConditionActive(key) && now.Sub(first) < time.Duration(config.DelaySeconds)*time.Second {
			continue
		}
		if previous, ok := w.lastRaised[key]; ok && previous.Equal(sample.ValueTime) {
			continue
		}
		name := source.Text["description"]
		if name == "" {
			name = config.PointID
		}
		detail := fmt.Sprintf("%s · 当前 %g · 条件 %s", name, sample.Value, config.Summary())
		var raised alarm.Alarm
		var err error
		if evidence != nil {
			raised, err = w.Alarms.RaiseObserved(now, key, alarm.Severity(config.Severity), config.Name, detail, deviceID, pointID, *evidence)
		} else {
			raised, err = w.Alarms.Raise(now, key, alarm.Severity(config.Severity), config.Name, detail, deviceID, pointID)
		}
		if err != nil || !raised.Active {
			continue
		}
		w.lastRaised[key] = sample.ValueTime
		if config.Confirm == "auto" && raised.State == alarm.Active {
			_, _ = w.Alarms.Acknowledge(now, raised.ID, "system")
		}
	}
}

func (w *Watcher) clearType(now time.Time, key string, recordRecovery bool, evidence *alarm.InputEvidence) {
	var cleared alarm.Alarm
	var err error
	if evidence != nil {
		cleared, err = w.Alarms.ClearObserved(now, key, *evidence)
	} else {
		cleared, err = w.Alarms.Clear(now, key)
	}
	if err != nil {
		return
	}
	if !recordRecovery {
		_, _ = w.Alarms.Close(now, cleared.ID)
	}
}

func channelOffline(view NodeView, now time.Time) bool {
	if view.Num["disabled"] != 0 {
		return false
	}
	since, found := view.Num["lastGoodPoll"]
	lastSuccess := time.Time{}
	if found && since > 0 {
		lastSuccess = time.Unix(int64(since), 0)
	}
	verdict := health.Evaluate(health.Input{
		Requests: 1, ConsecutiveFailures: 1, LastSuccess: lastSuccess,
		PollPeriod: time.Duration(view.Num["pollPeriod"]) * time.Millisecond,
		Timeout:    time.Duration(view.Num["timeout"]) * time.Millisecond,
		CreatedAt:  view.Created,
	}, now)
	return verdict.State == health.Offline
}
