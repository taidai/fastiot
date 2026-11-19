package pumphouse

import (
	"errors"
	"fmt"

	"fastiot/internal/ems/channel"
)

// Bus adapts the plant to channel.Bus, so tests and the runtime reach the
// simulator through the same polling and write path as a Modbus transport.
type Bus struct {
	plant *Plant
}

// Bus returns the in-process Modbus transport.
func (p *Plant) Bus() *Bus { return &Bus{plant: p} }

func (b *Bus) server(server byte, count uint16, limit uint16) error {
	if server != b.plant.cfg.Station {
		return fmt.Errorf("unknown station %d", server)
	}
	if count == 0 || count > limit {
		return fmt.Errorf("invalid count %d", count)
	}
	return nil
}

func (b *Bus) ReadCoils(server byte, address, count uint16) ([]bool, error) {
	if server != b.plant.cfg.Station {
		return nil, fmt.Errorf("unknown station %d", server)
	}
	return nil, fmt.Errorf("%w: coils are not mapped", ErrIllegalAddress)
}

func (b *Bus) ReadDiscreteInputs(server byte, address, count uint16) ([]bool, error) {
	if err := b.server(server, count, 2000); err != nil {
		return nil, err
	}
	b.plant.mu.Lock()
	defer b.plant.mu.Unlock()
	b.plant.advanceLocked(b.plant.now())
	bits := make([]bool, count)
	for i := range bits {
		value, err := b.plant.bitLocked(address + uint16(i))
		if err != nil {
			return nil, fmt.Errorf("%w: %d", err, address+uint16(i))
		}
		bits[i] = value
	}
	return bits, nil
}

func (b *Bus) ReadHoldingRegisters(server byte, address, count uint16) ([]uint16, error) {
	if err := b.server(server, count, 125); err != nil {
		return nil, err
	}
	b.plant.mu.Lock()
	defer b.plant.mu.Unlock()
	b.plant.advanceLocked(b.plant.now())
	words := make([]uint16, count)
	for i := range words {
		value, err := b.plant.holdingLocked(address + uint16(i))
		if err != nil {
			return nil, fmt.Errorf("%w: %d", err, address+uint16(i))
		}
		words[i] = value
	}
	return words, nil
}

func (b *Bus) ReadInputRegisters(server byte, address, count uint16) ([]uint16, error) {
	if err := b.server(server, count, 125); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: input registers are not mapped", ErrIllegalAddress)
}

func (b *Bus) WriteCoil(server byte, address uint16, value bool) error {
	if server != b.plant.cfg.Station {
		return fmt.Errorf("unknown station %d", server)
	}
	return fmt.Errorf("%w: coil %d", ErrIllegalAddress, address)
}

func (b *Bus) WriteHoldingRegister(server byte, address, value uint16) error {
	if server != b.plant.cfg.Station {
		return fmt.Errorf("unknown station %d", server)
	}
	b.plant.mu.Lock()
	defer b.plant.mu.Unlock()
	b.plant.advanceLocked(b.plant.now())
	err := b.plant.writeHoldingLocked(address, value)
	if errors.Is(err, ErrRefused) {
		return channel.Refuse(err.Error())
	}
	return err
}
