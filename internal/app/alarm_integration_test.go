package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fastiot/internal/ems/alarm"
	"github.com/simpleiot/simpleiot/data"
)

func TestApplicationEvaluatesAlarmFromLiveModbus(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns sync.Map
	var value, writes atomic.Uint32
	value.Store(45)
	t.Cleanup(func() { listener.Close(); conns.Range(func(key, _ any) bool { key.(net.Conn).Close(); return true }) })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conns.Store(conn, true)
			go func() {
				defer conn.Close()
				defer conns.Delete(conn)
				for {
					header := make([]byte, 7)
					if _, err := io.ReadFull(conn, header); err != nil {
						return
					}
					size := int(binary.BigEndian.Uint16(header[4:6]))
					if size < 2 || size > 254 {
						return
					}
					request := make([]byte, size-1)
					if _, err := io.ReadFull(conn, request); err != nil {
						return
					}
					reply := []byte{request[0] | 0x80, 1}
					if request[0] == 3 && len(request) == 5 {
						count := int(binary.BigEndian.Uint16(request[3:5]))
						if count < 1 || count > 125 {
							return
						}
						reply = make([]byte, 2+count*2)
						reply[0] = 3
						reply[1] = byte(count * 2)
						for i := 0; i < count; i++ {
							binary.BigEndian.PutUint16(reply[2+i*2:], uint16(value.Load()))
						}
					} else {
						writes.Add(1)
					}
					binary.BigEndian.PutUint16(header[4:6], uint16(len(reply)+1))
					if _, err := conn.Write(append(header, reply...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	password := data.NewID()
	t.Setenv("FASTEMS_ADMIN_PASSWORD", password)
	t.Setenv("FASTEMS_SEED_DEMO", "0")
	port, err := FreePort()
	if err != nil {
		t.Fatal(err)
	}
	application, err := Start(context.Background(), Config{DataDir: t.TempDir(), NATSPort: port, DisableHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	var session struct {
		Token string `json:"token"`
	}
	encoded, _ := json.Marshal(map[string]string{"email": "admin", "password": password})
	deadline := time.Now().Add(5 * time.Second)
	for {
		req := httptest.NewRequest("POST", "/api/login", bytes.NewReader(encoded))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		application.ServeHTTP(rec, req)
		if rec.Code == 503 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		if rec.Code != 200 {
			t.Fatalf("login: %d", rec.Code)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
			t.Fatal(err)
		}
		break
	}
	var snapshot struct {
		Roots []string `json:"roots"`
	}
	calculationAPI(t, application, "GET", "/snapshot", session.Token, nil, &snapshot)
	root := snapshot.Roots[0]
	var bus, point struct {
		ID string `json:"id"`
	}
	calculationAPI(t, application, "POST", "/modbus", session.Token, map[string]any{"parent": root, "description": "isolated read-only fixture", "protocol": "TCP", "uri": listener.Addr().String(), "poll_period": 100, "timeout": 500}, &bus)
	calculationAPI(t, application, "POST", "/modbus/"+bus.ID+"/io", session.Token, map[string]any{"description": "A temperature", "server_id": 1, "address": 10, "io_type": "modbusHoldingRegister", "data_format": "uint16", "scale": 1, "read_only": true, "units": "C"}, &point)
	calculationAPI(t, application, "PUT", "/alarm-types?root="+root, session.Token, map[string]any{"root": root, "type": map[string]any{"code": "TEMP_HIGH", "name": "A high", "severity": "warning", "trigger": "gt", "point_id": point.ID, "value": 40, "confirm": "manual", "record_recovery": true, "enabled": true}}, nil)
	wait := func(active bool) alarm.Alarm {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			var result struct {
				Alarms []alarm.Alarm `json:"alarms"`
			}
			calculationAPI(t, application, "GET", "/ems/alarms", session.Token, nil, &result)
			for _, record := range result.Alarms {
				if record.Title == "A high" && record.Active == active {
					return record
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("live alarm active=%v was not observed", active)
		return alarm.Alarm{}
	}
	first := wait(true)
	if first.PointID != point.ID {
		t.Fatal("alarm lost its physical point")
	}
	calculationAPI(t, application, "POST", "/ems/alarms/"+first.ID+"/ack", session.Token, map[string]any{"note": "已接手"}, nil)
	value.Store(20)
	recovered := wait(false)
	if recovered.ID != first.ID {
		t.Fatal("recovery targeted another occurrence")
	}
	value.Store(46)
	again := wait(true)
	if again.ID == first.ID || again.AckedBy != "" || writes.Load() != 0 {
		t.Fatal("recurrence inherited handling or wrote a device")
	}
}
