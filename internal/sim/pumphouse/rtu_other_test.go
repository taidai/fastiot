//go:build !linux

package pumphouse

import "testing"

// The virtual serial acceptance builds a Linux pty pair. Elsewhere the tests
// skip by name so a passing run is never read as serial-link coverage.
const virtualSerialSkip = "virtual serial RTU acceptance needs a Linux pty pair; run it in the CI container"

func TestRTUVirtualSerialFramesCRCAndStation(t *testing.T)  { t.Skip(virtualSerialSkip) }
func TestRTUVirtualSerialTimeoutAndRecovery(t *testing.T)   { t.Skip(virtualSerialSkip) }
func TestRTUVirtualSerialLateReadback(t *testing.T)         { t.Skip(virtualSerialSkip) }
func TestRTUVirtualSerialDisconnectAndRecover(t *testing.T) { t.Skip(virtualSerialSkip) }
func TestRTUVirtualSerialResponseStationGuard(t *testing.T) { t.Skip(virtualSerialSkip) }
func TestRTUVirtualSerialCRCRejection(t *testing.T)         { t.Skip(virtualSerialSkip) }
