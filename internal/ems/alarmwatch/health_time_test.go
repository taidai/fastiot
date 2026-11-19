package alarmwatch

import (
	"testing"
	"time"
)

func TestZeroLastGoodTimestampIsNoSuccess(t *testing.T) {
	bus := view("bus", "modbus")
	bus.Num["lastGoodPoll"] = -62135596800
	findings := Evaluate([]NodeView{bus}, now)
	finding, ok := find(findings, "bus-offline:bus")
	if !ok || finding.Detail != "no_success" {
		t.Fatalf("zero lastGoodPoll = %+v", findings)
	}
}

func TestFutureLastGoodTimestampIsOffline(t *testing.T) {
	bus := view("bus", "modbus")
	bus.Num["lastGoodPoll"] = float64(now.Add(time.Second).Unix())
	findings := Evaluate([]NodeView{bus}, now)
	finding, ok := find(findings, "bus-offline:bus")
	if !ok || finding.Detail != "invalid_time" {
		t.Fatalf("future lastGoodPoll = %+v", findings)
	}
}
