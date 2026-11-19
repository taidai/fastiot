package pumphouse

import (
	"encoding/binary"
)

// handlePDU applies one Modbus PDU to the plant and returns the response PDU.
// Both the TCP server and the RTU slave share it so the two transports cannot
// drift apart in what they accept.
func (p *Plant) handlePDU(unit byte, pdu []byte) []byte {
	function := pdu[0]
	if unit != p.cfg.Station {
		return exception(function, 2)
	}
	switch function {
	case 1, 2:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 2000 || int(address)+int(count)-1 > 65535 {
			return exception(function, 3)
		}
		bits, err := p.readBits(address, count)
		if err != nil {
			return exceptionFor(function, err)
		}
		return append([]byte{function, byte((len(bits) + 7) / 8)}, packBits(bits)...)
	case 3, 4:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		count := binary.BigEndian.Uint16(pdu[3:5])
		if count < 1 || count > 125 || int(address)+int(count)-1 > 65535 {
			return exception(function, 3)
		}
		if function == 4 {
			return exception(function, 2) // input registers are not mapped
		}
		words, err := p.readHolding(address, count)
		if err != nil {
			return exceptionFor(function, err)
		}
		response := make([]byte, 2, 2+len(words)*2)
		response[0] = function
		response[1] = byte(len(words) * 2)
		for _, word := range words {
			response = binary.BigEndian.AppendUint16(response, word)
		}
		return response
	case 5:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		return exception(function, 2) // coils are not mapped
	case 6:
		if len(pdu) != 5 {
			return exception(function, 3)
		}
		address := binary.BigEndian.Uint16(pdu[1:3])
		value := binary.BigEndian.Uint16(pdu[3:5])
		if err := p.writeHolding(address, value); err != nil {
			return exceptionFor(function, err)
		}
		return append([]byte(nil), pdu[:5]...)
	default:
		return exception(function, 1) // illegal function
	}
}

func (p *Plant) readBits(address, count uint16) ([]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	bits := make([]bool, count)
	for i := range bits {
		value, err := p.bitLocked(address + uint16(i))
		if err != nil {
			return nil, err
		}
		bits[i] = value
	}
	return bits, nil
}

func (p *Plant) readHolding(address, count uint16) ([]uint16, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	words := make([]uint16, count)
	for i := range words {
		value, err := p.holdingLocked(address + uint16(i))
		if err != nil {
			return nil, err
		}
		words[i] = value
	}
	return words, nil
}

func (p *Plant) writeHolding(address, value uint16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.advanceLocked(p.now())
	return p.writeHoldingLocked(address, value)
}
