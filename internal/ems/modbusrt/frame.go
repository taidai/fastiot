package modbusrt

import (
	"fmt"

	"fastiot/internal/ems/framelog"

	"github.com/simpleiot/simpleiot/modbus"
)

type recordingTransport struct {
	modbus.Transport
	peer string
}

func (t *recordingTransport) Read(p []byte) (int, error) {
	n, err := t.Transport.Read(p)
	if n > 0 {
		framelog.Default.Record(t.peer, "rx", append([]byte(nil), p[:n]...))
	}
	return n, err
}

func (t *recordingTransport) Write(p []byte) (int, error) {
	n, err := t.Transport.Write(p)
	if n > 0 {
		framelog.Default.Record(t.peer, "tx", append([]byte(nil), p[:n]...))
	}
	return n, err
}

// stationGuard rejects a well-formed frame from a different station. The
// upstream client decodes whatever arrives; on a shared RS485 line a
// duplicated address or noise would otherwise be accepted as this device's
// answer. Channel calls are serial, so the last encoded station is the one a
// response must carry.
type stationGuard struct {
	modbus.Transport
	pending byte
}

func (g *stationGuard) Encode(id byte, pdu modbus.PDU) ([]byte, error) {
	g.pending = id
	return g.Transport.Encode(id, pdu)
}

func (g *stationGuard) Decode(packet []byte) (byte, modbus.PDU, error) {
	id, pdu, err := g.Transport.Decode(packet)
	if err != nil {
		return id, pdu, err
	}
	if id != g.pending {
		return id, pdu, fmt.Errorf("rtu: response station %d does not match request station %d", id, g.pending)
	}
	return id, pdu, nil
}
