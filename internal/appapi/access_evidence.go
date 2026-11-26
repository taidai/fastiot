package appapi

import (
	"fmt"
	"strconv"
	"time"

	"fastiot/internal/ems/bridge"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/profile"
	"github.com/simpleiot/simpleiot/data"
)

// L1 probes a declared address, not register zero (which many devices lack).
func probeProfileUnit(connection ModbusConnectionInput, server int, spec profile.PointSpec) LineTestResult {
	result := LineTestResult{Probed: 1, Stations: []LineTestStation{{ServerID: server, Probe: fmt.Sprintf("%s:%d", spec.Function, spec.Address)}}}
	if connection.Disabled {
		result.Stations[0].Error = "通道已停用"
		return result
	}
	transport, closeFn, err := openModbusClient(connection)
	if err != nil {
		result.Stations[0].Error = err.Error()
		return result
	}
	defer closeFn()
	result.Connected = true
	start := time.Now()
	count := uint16(1)
	if spec.Format == "uint32" || spec.Format == "int32" || spec.Format == "float32" {
		count = 2
	}
	switch spec.Function {
	case channel.Coil:
		_, err = transport.ReadCoils(byte(server), spec.Address, 1)
	case channel.DiscreteInput:
		_, err = transport.ReadDiscreteInputs(byte(server), spec.Address, 1)
	case channel.InputRegister:
		_, err = transport.ReadInputRegs(byte(server), spec.Address, count)
	default:
		_, err = transport.ReadHoldingRegs(byte(server), spec.Address, count)
	}
	result.LatencyMS = time.Since(start).Milliseconds()
	result.Stations[0].LatencyMS = result.LatencyMS
	result.Stations[0].Responded = err == nil
	if err != nil {
		result.Stations[0].Error = err.Error()
	}
	return result
}

// Evidence binds the Profile content, model revision, connection and acquisition
// revisions. Runtime samples/health never affect this configuration identity.
func (h *Handler) accessBasis(channel data.NodeEdge, server int) string {
	if h.ems == nil {
		return ""
	}
	return accessBasisFromPoints(channel, server, h.unitPoints(channel.ID), h.ems.Profiles)
}

// accessBasisFromPoints is accessBasis for callers that already loaded the
// channel's points, so listing many units does not repeat the same reads.
func accessBasisFromPoints(channel data.NodeEdge, server int, points []unitModelPointView, registry *profile.Registry) string {
	if points == nil || registry == nil {
		return ""
	}
	model, configured := unitModelFor(channel, server)
	if !configured {
		return ""
	}
	version, found := registry.Fingerprint(model.ProfileID)
	if !found {
		return ""
	}
	stamps := map[string]string{}
	for _, point := range points {
		if point.ServerID == server {
			stamps[point.ID] = point.ConfigStamp
		}
	}
	revision, _ := channel.Points.Find(unitModelPoint, strconv.Itoa(server))
	return accessHash(struct {
		Version    string
		Revision   data.Point
		Connection string
		Points     map[string]string
	}{version, revision, bridge.AcquisitionStamp(channel, data.NodeEdge{}), stamps})
}
