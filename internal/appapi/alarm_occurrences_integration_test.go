package appapi

import (
	"net/http"
	"testing"
	"time"

	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/runtime"
	"fastiot/internal/emsapi"
	"github.com/simpleiot/simpleiot/data"
)

func TestAlarmStatisticsCountIndependentOccurrences(t *testing.T) {
	base, token, h, _ := startInputServer(t, t.TempDir(), data.NewID())
	service := runtime.NewService(nil, time.Minute, time.Second, 100)
	defer service.Close()
	h.SetEMS(emsapi.New(service, time.Minute))
	root := snapshotRoot(t, base, token)
	device := createNodeAPI(t, base, token, "group", root, "A", 0)
	point := createNodeAPI(t, base, token, "variable", device, "temperature", 0)
	now := time.Now()
	first, err := service.Alarms.Raise(now, "temperature:A", alarm.Warning, "温度异常", "46", device, point)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Alarms.Clear(now.Add(time.Millisecond), first.Key); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Alarms.Raise(now.Add(2*time.Millisecond), first.Key, alarm.Warning, "温度异常", "48", device, point); err != nil {
		t.Fatal(err)
	}
	var stats AlarmStats
	status, body := call(t, http.MethodGet, base+"/alarm-stats?days=1", token, nil, &stats)
	if status != 200 || stats.Today.Raised != 2 || stats.Today.Unhandled != 1 {
		t.Fatalf("independent occurrences collapsed in statistics: %d %s", status, body)
	}
}
