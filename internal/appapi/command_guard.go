package appapi

import (
	"time"

	"fastiot/internal/decision"
	"fastiot/internal/ems/command"
	"fastiot/internal/ems/device"
	"fastiot/internal/ems/safety"
	"fastiot/internal/ems/signal"
	siotclient "github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// commandGuard resolves the persisted target and its safety envelope at the
// product boundary. The command manager calls it once for every normalized write.
func (h *Handler) commandGuard(request command.GuardRequest) safety.Verdict {
	if len(request.Writes) != 1 {
		return safety.Verdict{Decision: "refused", Reason: "invalid_command"}
	}
	write := request.Writes[0]
	now := time.Now()
	req := safety.Request{
		Output: write.Point, Requested: write.Value, Automatic: request.Automatic,
		Samples: map[string]signal.Sample{},
	}
	// Without a mounted runtime there are no samples, so conditions fail
	// closed; the envelope itself (range, capabilities) still applies.
	if h.ems != nil && h.ems.Service != nil {
		req.MaxAge = h.ems.Service.MaxAge
		for _, value := range h.ems.Service.State.Snapshot(now, h.ems.Service.MaxAge) {
			req.Samples[value.PointID] = signal.Sample{
				Input: value.PointID, Value: value.Value, Quality: value.Quality,
				SourceTime: value.SourceTime, ReceivedTime: value.ReceivedTime,
			}
		}
	}
	nodes, err := siotclient.GetNodes(h.nc, "all", write.Point, "", false)
	if err != nil || len(nodes) == 0 {
		return safety.Evaluate(req, now)
	}
	node := nodes[0]
	disabled, _ := node.Points.Value(data.PointTypeDisabled, "")
	readOnly, _ := node.Points.Value(data.PointTypeReadOnly, "")
	manual, _ := node.Points.Value("easyemsManual", "")
	req.Disabled = disabled != 0
	req.ReadOnly = readOnly != 0
	req.Manual = manual != 0
	if node.Type == data.NodeTypeModbusIO {
		ioType, _ := node.Points.Text(data.PointTypeModbusIOType, "")
		if ioType != data.PointValueModbusCoil && ioType != data.PointValueModbusHoldingRegister {
			req.ReadOnly = true
		}
	}
	config, present, err := decision.LoadOutputSafety(h.nc, write.Point)
	if err != nil {
		return safety.Verdict{Decision: "refused", Value: write.Value.Num, Reason: "invalid_safety_config"}
	}
	if present {
		spec, err := config.Output()
		if err != nil {
			return safety.Verdict{Decision: "refused", Value: write.Value.Num, Reason: "invalid_safety_config"}
		}
		req.Spec = &spec
	}
	return safety.Evaluate(req, now)
}

// manualWriteVerdict judges one operator point write with the resolver the
// command manager uses, so the point table cannot bypass the safety envelope.
func (h *Handler) manualWriteVerdict(pointID string, value float64) safety.Verdict {
	return h.commandGuard(command.GuardRequest{
		Point:  pointID,
		Writes: []device.Write{{Point: pointID, Value: signal.Num(value)}},
	})
}
