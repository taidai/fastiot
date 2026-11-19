// Command pumphouse runs the two-pump one-tank SCADA sample without the paid
// EMS package: it starts the simulated station and a clean fastiot instance,
// then builds the sample project through the product HTTP API exactly as a
// delivery engineer would. Nothing here talks to real hardware.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/app"
	"fastiot/internal/ems/modbusrt"
	"fastiot/internal/sim/pumphouse"
)

func main() {
	dataDir := flag.String("data", "pumphouse-data", "sample data directory (use an empty directory for a fresh demo)")
	httpPort := flag.String("port", "8080", "HTTP port of the sample instance")
	natsPort := flag.Int("nats-port", 4222, "embedded NATS port")
	simPort := flag.Int("sim-port", 15020, "Modbus TCP port of the simulated station")
	simRTU := flag.String("sim-rtu", "", "serve the simulated station as a Modbus RTU slave on this port (port@baud); requires -rtu-bus")
	rtuBus := flag.String("rtu-bus", "", "serial address (port@baud) the sample channel polls; requires -sim-rtu")
	controlAddr := flag.String("control", "127.0.0.1:15021", "simulator control address for fault/local injection")
	password := flag.String("password", envOr("FASTEMS_ADMIN_PASSWORD", "pumphouse-demo-2026"), "admin password (at least 12 characters)")
	flag.Parse()

	if len(*password) < 12 {
		log.Fatal("the admin password must contain at least 12 characters")
	}
	if (*simRTU == "") != (*rtuBus == "") {
		log.Fatal("-sim-rtu and -rtu-bus must be given together: one end of a serial pair is the station, the other is the bus")
	}
	if err := prepareProfile(*dataDir); err != nil {
		log.Fatal(err)
	}

	plant := pumphouse.New(pumphouse.DefaultConfig())
	station, err := plant.ServeTCP(fmt.Sprintf("127.0.0.1:%d", *simPort))
	if err != nil {
		log.Fatalf("start simulated station: %v", err)
	}
	defer station.Close()
	channelProtocol := "TCP"
	channelAddress := fmt.Sprintf("127.0.0.1:%d", *simPort)
	var rtuStation *pumphouse.RTUServer
	if *simRTU != "" {
		cfg, err := modbusrt.ParseSerialAddress(*simRTU)
		if err != nil {
			log.Fatalf("-sim-rtu: %v", err)
		}
		port, err := modbusrt.OpenSerialPort(cfg)
		if err != nil {
			log.Fatalf("open simulated station serial port %s: %v", cfg.Port, err)
		}
		rtuStation = plant.ServeRTU(port, 0)
		defer rtuStation.Close()
		channelProtocol = "RTU"
		channelAddress = *rtuBus
	}
	control := serveControl(plant, rtuStation, *controlAddr)
	defer control.Close()

	// Bootstrap reads this env var on first start; keep the flag authoritative.
	_ = os.Setenv("FASTEMS_ADMIN_PASSWORD", *password)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("simulated station (NOT real hardware) listening on Modbus TCP %s", station.Addr())
	if rtuStation != nil {
		log.Printf("simulated station (NOT real hardware) also serving Modbus RTU on %s; the sample channel polls %s", *simRTU, channelAddress)
	}
	application, err := app.Start(ctx, app.Config{
		DataDir: *dataDir, HTTPPort: *httpPort, NATSPort: *natsPort, Version: "pumphouse-sample",
	})
	if err != nil {
		log.Fatal(err)
	}
	defer application.Close()

	base := fmt.Sprintf("http://127.0.0.1:%s/api", *httpPort)
	token, err := login(base, "admin", *password)
	if err != nil {
		log.Fatalf("sign in to the sample instance: %v\n"+
			"If %s already holds another project, point -data at an empty directory or pass its admin password.", err, *dataDir)
	}
	api := &apiClient{base: base, token: token, http: &http.Client{Timeout: 10 * time.Second}}
	sample, err := seedSample(api, channelProtocol, channelAddress)
	if err != nil {
		log.Fatalf("build the pump house sample: %v", err)
	}
	printSummary(api, sample, *httpPort)
	if rtuStation != nil {
		log.Printf("  RTU link faults: curl -X POST http://%s/link -d '{\"mute\":true}' (also delay_ms, corrupt_response_crc, corrupt_request_crc; GET for faults and counters)", *controlAddr)
	}

	select {
	case <-ctx.Done():
	case <-application.Exited():
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// prepareProfile installs the sample device profile into the data directory
// before the instance loads profiles at startup.
func prepareProfile(dataDir string) error {
	dir := filepath.Join(dataDir, "profiles")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	encoded, err := pumphouse.SampleProfileJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, pumphouse.ProfileID+".json"), encoded, 0600)
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *apiClient) request(method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %d %s", method, path, response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return payload, nil
}

func (c *apiClient) json(method, path string, body, target any) error {
	payload, err := c.request(method, path, body)
	if err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	return json.Unmarshal(payload, target)
}

func login(base, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	response, err := (&http.Client{Timeout: 5 * time.Second}).Post(base+"/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login returned %d", response.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Token == "" {
		return "", errors.New("login returned an empty token")
	}
	return result.Token, nil
}

type sampleIDs struct {
	Group    string
	Channel  string
	Level    string
	PointIDs map[string]string
}

func seedSample(api *apiClient, protocol, address string) (sampleIDs, error) {
	var snapshot struct {
		Roots []string        `json:"roots"`
		Nodes []data.NodeEdge `json:"nodes"`
	}
	if err := api.json(http.MethodGet, "/snapshot", nil, &snapshot); err != nil {
		return sampleIDs{}, err
	}
	if len(snapshot.Roots) != 1 {
		return sampleIDs{}, fmt.Errorf("expected one project root, got %d", len(snapshot.Roots))
	}
	for _, node := range snapshot.Nodes {
		if node.Type == data.NodeTypeGroup && node.Desc() == pumphouse.SampleName {
			log.Printf("pump house group already exists in this data directory; leaving it unchanged")
			return sampleIDs{Group: node.ID}, nil
		}
	}

	var group struct {
		ID string `json:"id"`
	}
	if err := api.json(http.MethodPost, "/nodes", map[string]any{
		"parent": snapshot.Roots[0], "type": "group", "description": pumphouse.SampleName,
	}, &group); err != nil {
		return sampleIDs{}, err
	}

	var channel struct {
		ID string `json:"id"`
	}
	transport := "Modbus TCP"
	connection := map[string]any{
		"parent": group.ID, "protocol": protocol,
		"poll_period": 1000, "timeout": 1000,
	}
	if protocol == "RTU" {
		serial, err := modbusrt.ParseSerialAddress(address)
		if err != nil {
			return sampleIDs{}, err
		}
		transport = "Modbus RTU"
		connection["port"] = serial.Port
		connection["baud"] = strconv.Itoa(serial.Baud)
		connection["data_bits"] = serial.DataBits
		connection["parity"] = serial.Parity
		connection["stop_bits"] = serial.StopBits
	} else {
		connection["uri"] = address
	}
	connection["description"] = "泵站模拟器（" + transport + "，非真实设备）"
	if err := api.json(http.MethodPost, "/modbus", connection, &channel); err != nil {
		return sampleIDs{}, err
	}

	request, err := http.NewRequest(http.MethodPost, api.base+"/modbus/"+channel.ID+"/io/import",
		bytes.NewReader(pumphouse.PointTableCSV(1)))
	if err != nil {
		return sampleIDs{}, err
	}
	request.Header.Set("Authorization", "Bearer "+api.token)
	request.Header.Set("Content-Type", "text/csv; charset=utf-8")
	response, err := api.http.Do(request)
	if err != nil {
		return sampleIDs{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(response.Body)
		return sampleIDs{}, fmt.Errorf("import point table: %d %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}

	var model struct {
		Points []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"points"`
	}
	if err := api.json(http.MethodGet, "/modbus/"+channel.ID+"/units/1/model", nil, &model); err != nil {
		return sampleIDs{}, err
	}
	pointIDs := map[string]string{}
	for _, point := range model.Points {
		pointIDs[point.Name] = point.ID
	}
	if len(pointIDs) != len(pumphouse.SamplePoints) {
		return sampleIDs{}, fmt.Errorf("point table has %d points, want %d", len(pointIDs), len(pumphouse.SamplePoints))
	}
	if err := api.json(http.MethodPut, "/modbus/"+channel.ID+"/units/1/model", map[string]any{
		"name": "泵站模拟控制器（模拟）", "profile_id": pumphouse.ProfileID, "point_map": pointIDs,
	}, nil); err != nil {
		return sampleIDs{}, err
	}

	tiles := make([]string, 0, len(pumphouse.ScreenTiles))
	for _, name := range pumphouse.ScreenTiles {
		tiles = append(tiles, pointIDs[name])
	}
	if err := api.json(http.MethodPut, "/ems/screen/"+group.ID, map[string]any{
		"title": pumphouse.SampleName, "columns": 4, "point_ids": tiles,
	}, nil); err != nil {
		return sampleIDs{}, err
	}

	for _, config := range pumphouse.AlarmConfigs(pointIDs) {
		if err := api.json(http.MethodPut, "/alarm-types?root="+group.ID, map[string]any{
			"root": group.ID, "type": config,
		}, nil); err != nil {
			return sampleIDs{}, err
		}
	}
	for _, name := range []string{pumphouse.PointPump1Command, pumphouse.PointPump2Command} {
		if err := api.json(http.MethodPut, "/safety", map[string]any{
			"node": pointIDs[name], "safety": pumphouse.StartSafety(pointIDs[pumphouse.PointLevel]),
		}, nil); err != nil {
			return sampleIDs{}, err
		}
	}

	sample := sampleIDs{Group: group.ID, Channel: channel.ID, Level: pointIDs[pumphouse.PointLevel], PointIDs: pointIDs}
	if err := waitForValues(api, sample.Level); err != nil {
		return sample, err
	}
	return sample, nil
}

// waitForValues waits until the runtime has polled the station once.
func waitForValues(api *apiClient, levelID string) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var state struct {
			Values []struct {
				PointID string `json:"point_id"`
				Value   struct {
					Number *float64 `json:"number"`
				} `json:"value"`
			} `json:"values"`
		}
		if err := api.json(http.MethodGet, "/ems/state", nil, &state); err == nil {
			for _, value := range state.Values {
				if value.PointID == levelID && value.Value.Number != nil {
					return nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the simulated station produced no values; check the channel")
}

func printSummary(api *apiClient, sample sampleIDs, port string) {
	log.Printf("sample project ready (simulated device, NOT real hardware)")
	log.Printf("  open http://127.0.0.1:%s/ and sign in as admin", port)
	log.Printf("  running screen: 画面 → %s", pumphouse.SampleName)
	log.Printf("  commands: 点位表/设备页 → %s / %s", pumphouse.PointPump1Command, pumphouse.PointPump2Command)
	if len(sample.PointIDs) == 0 {
		log.Printf("  the sample group already existed; no readings were refreshed")
	} else {
		readings := stateReadings(api)
		log.Printf("  level=%s%% pressure=%s MPa pump1=%s pump2=%s",
			readings[sample.PointIDs[pumphouse.PointLevel]],
			readings[sample.PointIDs[pumphouse.PointPressure]],
			readings[sample.PointIDs[pumphouse.PointPump1Run]],
			readings[sample.PointIDs[pumphouse.PointPump2Run]])
	}
	log.Printf("  fault injection: curl -X POST http://127.0.0.1:15021/pump/1/fault -d '{\"active\":true}'")
	log.Printf("  local mode:      curl -X POST http://127.0.0.1:15021/pump/1/local -d '{\"active\":true}'")
	log.Printf("  simulator state: curl http://127.0.0.1:15021/state")
}

func stateReadings(api *apiClient) map[string]string {
	var state struct {
		Values []struct {
			PointID string `json:"point_id"`
			Value   struct {
				Number *float64 `json:"number"`
				Bool   *bool    `json:"bool"`
			} `json:"value"`
		} `json:"values"`
	}
	readings := map[string]string{}
	if err := api.json(http.MethodGet, "/ems/state", nil, &state); err != nil {
		return readings
	}
	for _, value := range state.Values {
		switch {
		case value.Value.Number != nil:
			readings[value.PointID] = fmt.Sprintf("%g", *value.Value.Number)
		case value.Value.Bool != nil:
			if *value.Value.Bool {
				readings[value.PointID] = "1"
			} else {
				readings[value.PointID] = "0"
			}
		}
	}
	return readings
}

// serveControl exposes the simulator-only fault and local/remote switches so a
// person can demonstrate refusal without touching the project configuration.
func serveControl(plant *pumphouse.Plant, rtu *pumphouse.RTUServer, addr string) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/state", func(w http.ResponseWriter, r *http.Request) {
		writeControl(w, plant.Snapshot())
	})
	mux.HandleFunc("/pump/1/fault", faultHandler(plant, 1))
	mux.HandleFunc("/pump/2/fault", faultHandler(plant, 2))
	mux.HandleFunc("/pump/1/local", localHandler(plant, 1))
	mux.HandleFunc("/pump/2/local", localHandler(plant, 2))
	if rtu != nil {
		mux.HandleFunc("/link", linkHandler(rtu))
	}
	server := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("simulator control: %v", err)
		}
	}()
	return server
}

// linkChange changes only the fields present in the body, so a caller can set
// one fault without clearing the others.
type linkChange struct {
	Mute               *bool `json:"mute"`
	DelayMS            *int  `json:"delay_ms"`
	CorruptResponseCRC *bool `json:"corrupt_response_crc"`
	CorruptRequestCRC  *bool `json:"corrupt_request_crc"`
}

// linkHandler repeats the serial link faults the RTU acceptance covers, so a
// person can watch the same behaviour on a real or virtual serial line.
func linkHandler(rtu *pumphouse.RTUServer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeControl(w, map[string]any{"faults": rtu.Faults(), "stats": rtu.Stats()})
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
			return
		}
		var change linkChange
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&change); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		faults := rtu.Faults()
		if change.Mute != nil {
			faults.Mute = *change.Mute
		}
		if change.DelayMS != nil {
			faults.ResponseDelay = time.Duration(*change.DelayMS) * time.Millisecond
		}
		if change.CorruptResponseCRC != nil {
			faults.CorruptResponseCRC = *change.CorruptResponseCRC
		}
		if change.CorruptRequestCRC != nil {
			faults.CorruptRequestCRC = *change.CorruptRequestCRC
		}
		rtu.SetFaults(faults)
		writeControl(w, map[string]any{"faults": rtu.Faults(), "stats": rtu.Stats()})
	}
}

func faultHandler(plant *pumphouse.Plant, pump int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		change, ok := readControlChange(w, r)
		if !ok {
			return
		}
		plant.SetFault(pump, change.Active)
		writeControl(w, plant.Snapshot())
	}
}

func localHandler(plant *pumphouse.Plant, pump int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		change, ok := readControlChange(w, r)
		if !ok {
			return
		}
		plant.SetLocal(pump, change.Active)
		writeControl(w, plant.Snapshot())
	}
}

type controlChange struct {
	Active bool `json:"active"`
}

func readControlChange(w http.ResponseWriter, r *http.Request) (controlChange, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return controlChange{}, false
	}
	var change controlChange
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&change); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return controlChange{}, false
	}
	return change, true
}

func writeControl(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
