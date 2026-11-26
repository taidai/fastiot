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
	project := flag.Bool("project", true, "also build the full teaching project (site, area, devices, unit, screens, rule, state machine, published version)")
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
	if *project {
		teaching, err := seedTeachingProject(api, fmt.Sprintf("127.0.0.1:%d", *simPort))
		if err != nil {
			log.Fatalf("build the teaching project: %v", err)
		}
		printProjectSummary(teaching, *httpPort)
	} else {
		sample, err := seedSample(api, channelProtocol, channelAddress)
		if err != nil {
			log.Fatalf("build the pump house sample: %v", err)
		}
		printSummary(api, sample, *httpPort)
	}
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

	if err := importPointTable(api, channel.ID, pumphouse.PointTableCSV(1)); err != nil {
		return sampleIDs{}, err
	}
	pointIDs, err := unitPointIDs(api, channel.ID)
	if err != nil {
		return sampleIDs{}, err
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

// The teaching project mirrors the prototype's shape: Site → Area → Devices,
// a Unit on the Site, and a published rule/state machine/screen per layer. It
// is built only through the product HTTP API, never by writing store nodes.
const (
	projectSiteName    = "教学泵房"
	projectAreaName    = "泵房"
	projectUnitName    = "供水单元"
	projectPump1       = "1号泵"
	projectPump2       = "2号泵"
	projectTank        = "水箱"
	projectRuleName    = "供水低液位启动 1号泵"
	projectMachine     = "1号泵过程状态机"
	projectVersionNote = "教学泵房初始工程（设备、策略、画面）"
)

type projectResult struct {
	Site      string
	Area      string
	Unit      string
	Devices   map[string]string
	Channels  []string
	RuleID    string
	MachineID string
	VersionID string
	Existing  bool
}

// seedTeachingProject builds the full teaching project above the legacy sample
// group. It is idempotent at the project level: if the Site already exists the
// whole project is left untouched.
func seedTeachingProject(api *apiClient, simAddress string) (projectResult, error) {
	var snapshot struct {
		Roots []string        `json:"roots"`
		Nodes []data.NodeEdge `json:"nodes"`
	}
	if err := api.json(http.MethodGet, "/snapshot", nil, &snapshot); err != nil {
		return projectResult{}, err
	}
	if len(snapshot.Roots) != 1 {
		return projectResult{}, fmt.Errorf("expected one project root, got %d", len(snapshot.Roots))
	}
	root := snapshot.Roots[0]
	for _, node := range snapshot.Nodes {
		if node.Type == "site" && node.Desc() == projectSiteName {
			log.Printf("teaching project %q already exists in this data directory; leaving it unchanged (already exists)", projectSiteName)
			return projectResult{Site: node.ID, Existing: true}, nil
		}
	}

	site, err := createBusinessNode(api, root, "site", projectSiteName)
	if err != nil {
		return projectResult{}, err
	}
	area, err := createBusinessNode(api, site, "area", projectAreaName)
	if err != nil {
		return projectResult{}, err
	}
	devices := map[string]string{}
	for _, name := range []string{projectPump1, projectPump2, projectTank} {
		id, err := createBusinessNode(api, area, "device", name)
		if err != nil {
			return projectResult{}, err
		}
		devices[name] = id
	}
	unit, err := createBusinessNode(api, site, "unit", projectUnitName)
	if err != nil {
		return projectResult{}, err
	}
	if err := api.json(http.MethodPatch, "/nodes/"+unit, map[string]any{
		"description":     projectUnitName,
		"members_version": 0,
		"members": []map[string]any{
			{"role": "驱动泵", "node_id": devices[projectPump1], "required": true},
			{"role": "备用泵", "node_id": devices[projectPump2], "required": false},
			{"role": "液位反馈", "node_id": devices[projectTank], "required": true},
		},
	}, nil); err != nil {
		return projectResult{}, err
	}

	// One live channel per device. The simulated station answers only station 1,
	// so three channels to the same station keep every device online while still
	// giving each device its own unit model and point map.
	pump1, err := buildProjectChannel(api, site, simAddress, "泵站模拟器 · 1号泵（模拟设备）", "1号泵（模拟）", devices[projectPump1],
		[]string{pumphouse.PointPump1Current, pumphouse.PointPump1Command, pumphouse.PointPump1Starts,
			pumphouse.PointPump1Run, pumphouse.PointPump1Remote, pumphouse.PointPump1Fault})
	if err != nil {
		return projectResult{}, err
	}
	pump2, err := buildProjectChannel(api, site, simAddress, "泵站模拟器 · 2号泵（模拟设备）", "2号泵（模拟）", devices[projectPump2],
		[]string{pumphouse.PointPump2Current, pumphouse.PointPump2Command, pumphouse.PointPump2Starts,
			pumphouse.PointPump2Run, pumphouse.PointPump2Remote, pumphouse.PointPump2Fault})
	if err != nil {
		return projectResult{}, err
	}
	tank, err := buildProjectChannel(api, site, simAddress, "泵站模拟器 · 水箱（模拟设备）", "水箱（模拟）", devices[projectTank],
		[]string{pumphouse.PointLevel, pumphouse.PointPressure})
	if err != nil {
		return projectResult{}, err
	}

	levelID := tank.Points[pumphouse.PointLevel]
	pressureID := tank.Points[pumphouse.PointPressure]
	// Safety conditions are validated against the command point's own channel
	// subtree, so each pump uses the level point imported on its channel.
	for command, safetyLevel := range map[string]string{
		pump1.Points[pumphouse.PointPump1Command]: pump1.Points[pumphouse.PointLevel],
		pump2.Points[pumphouse.PointPump2Command]: pump2.Points[pumphouse.PointLevel],
	} {
		if err := api.json(http.MethodPut, "/safety", map[string]any{
			"node": command, "safety": pumphouse.StartSafety(safetyLevel),
		}, nil); err != nil {
			return projectResult{}, err
		}
	}

	metrics := []map[string]any{
		{"point_id": levelID, "label": "水箱液位", "target": "≥ 20"},
		{"point_id": pressureID, "label": "出口压力", "target": "0.35"},
		{"point_id": pump1.Points[pumphouse.PointPump1Run], "label": "1号泵运行", "target": ""},
		{"point_id": pump2.Points[pumphouse.PointPump2Run], "label": "2号泵运行", "target": ""},
	}
	recentIDs := []string{levelID, pressureID, pump1.Points[pumphouse.PointPump1Run], pump2.Points[pumphouse.PointPump2Run]}
	// Match frontend defaultScreen: 现场 uses flow, 区域 uses children; both
	// otherwise share the same nine blocks.
	if err := saveProjectScreen(api, site, projectSiteName, "flow", metrics, recentIDs, levelID); err != nil {
		return projectResult{}, err
	}
	if err := saveProjectScreen(api, area, projectAreaName, "children", metrics, recentIDs, levelID); err != nil {
		return projectResult{}, err
	}

	ruleID, err := seedProjectRule(api, site, levelID, pump1.Points[pumphouse.PointPump1Command])
	if err != nil {
		return projectResult{}, err
	}
	machineID, err := seedProjectMachine(api, site, devices[projectPump1])
	if err != nil {
		return projectResult{}, err
	}

	if err := waitForProjectValues(api, []string{
		levelID, pressureID,
		pump1.Points[pumphouse.PointPump1Run], pump1.Points[pumphouse.PointPump1Command],
		pump2.Points[pumphouse.PointPump2Run], pump2.Points[pumphouse.PointPump2Command],
	}); err != nil {
		return projectResult{}, err
	}

	versionID, err := publishProjectVersion(api, site)
	if err != nil {
		return projectResult{}, err
	}
	return projectResult{
		Site: site, Area: area, Unit: unit, Devices: devices,
		Channels: []string{pump1.ChannelID, pump2.ChannelID, tank.ChannelID},
		RuleID:   ruleID, MachineID: machineID, VersionID: versionID,
	}, nil
}

func createBusinessNode(api *apiClient, parent, nodeType, description string) (string, error) {
	var created struct {
		ID string `json:"id"`
	}
	if err := api.json(http.MethodPost, "/nodes", map[string]any{
		"parent": parent, "type": nodeType, "description": description,
	}, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("create %s %q returned no id", nodeType, description)
	}
	return created.ID, nil
}

type projectChannel struct {
	ChannelID string
	Points    map[string]string
}

// buildProjectChannel creates a live Modbus TCP channel to the simulated
// station and binds its unit model to one business device. The model is saved
// three times: profile+map, then the business binding, then an explicit
// simulated-device verification that clears the binding's "verify again" gate.
func buildProjectChannel(api *apiClient, parent, address, description, modelName, deviceID string, pointNames []string) (projectChannel, error) {
	var channel struct {
		ID string `json:"id"`
	}
	if err := api.json(http.MethodPost, "/modbus", map[string]any{
		"parent": parent, "protocol": "TCP", "uri": address,
		"poll_period": 1000, "timeout": 1000, "description": description,
	}, &channel); err != nil {
		return projectChannel{}, err
	}
	if channel.ID == "" {
		return projectChannel{}, errors.New("create modbus channel returned no id")
	}
	if err := importPointTable(api, channel.ID, pumphouse.PointTableCSV(1)); err != nil {
		return projectChannel{}, err
	}
	ids, err := unitPointIDs(api, channel.ID)
	if err != nil {
		return projectChannel{}, err
	}
	pointMap := make(map[string]string, len(pointNames))
	for _, name := range pointNames {
		if ids[name] == "" {
			return projectChannel{}, fmt.Errorf("channel %q: point %q missing after import", description, name)
		}
		pointMap[name] = ids[name]
	}
	path := "/modbus/" + channel.ID + "/units/1/model"
	base := map[string]any{"name": modelName, "profile_id": pumphouse.ProfileID, "point_map": pointMap}
	if err := api.json(http.MethodPut, path, base, nil); err != nil {
		return projectChannel{}, err
	}
	bound := map[string]any{"name": modelName, "profile_id": pumphouse.ProfileID, "point_map": pointMap, "business_node_id": deviceID}
	if err := api.json(http.MethodPut, path, bound, nil); err != nil {
		return projectChannel{}, err
	}
	// Binding the business node changes the mapping hash, so the follow-up save
	// that clears the re-verification gate has to echo the stored mapping and
	// profile versions.
	mappingVersion, profileVersion, err := unitModelVersions(api, channel.ID)
	if err != nil {
		return projectChannel{}, err
	}
	verified := map[string]any{
		"name": modelName, "profile_id": pumphouse.ProfileID, "point_map": pointMap, "business_node_id": deviceID,
		"mapping_version": mappingVersion, "profile_version": profileVersion,
		"verified": true, "verifier": "pumphouse-sample", "verified_at": time.Now().UTC().Format("2006-01-02"),
		"device_serial": "SIM-" + channel.ID, "verification": "软件模拟设备自检（地址/缩放/回读一致）；不代表真机验证",
	}
	if err := api.json(http.MethodPut, path, verified, nil); err != nil {
		return projectChannel{}, err
	}
	return projectChannel{ChannelID: channel.ID, Points: ids}, nil
}

func unitModelVersions(api *apiClient, channelID string) (string, string, error) {
	var view struct {
		Model struct {
			MappingVersion string `json:"mapping_version"`
			ProfileVersion string `json:"profile_version"`
		} `json:"model"`
	}
	if err := api.json(http.MethodGet, "/modbus/"+channelID+"/units/1/model", nil, &view); err != nil {
		return "", "", err
	}
	if view.Model.MappingVersion == "" {
		return "", "", errors.New("unit model returned no mapping_version")
	}
	return view.Model.MappingVersion, view.Model.ProfileVersion, nil
}

func importPointTable(api *apiClient, channelID string, csv []byte) error {
	request, err := http.NewRequest(http.MethodPost, api.base+"/modbus/"+channelID+"/io/import", bytes.NewReader(csv))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+api.token)
	request.Header.Set("Content-Type", "text/csv; charset=utf-8")
	response, err := api.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(response.Body)
		return fmt.Errorf("import point table: %d %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return nil
}

func unitPointIDs(api *apiClient, channelID string) (map[string]string, error) {
	var model struct {
		Points []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"points"`
	}
	if err := api.json(http.MethodGet, "/modbus/"+channelID+"/units/1/model", nil, &model); err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, point := range model.Points {
		ids[point.Name] = point.ID
	}
	return ids, nil
}

// saveProjectScreen writes the same nine-block default layout the screen
// configurator creates for a Site/Area, so this seed never replaces a complete
// default with a partial one. The layer-specific middle block is "flow" for a
// Site and "children" for an Area, matching defaultScreen.
func saveProjectScreen(api *apiClient, layerID, title, middleBlock string, metrics []map[string]any, recentIDs []string, trendID string) error {
	middleBinding := map[string]any{}
	if middleBlock == "flow" {
		middleBinding["detail"] = true
	}
	return api.json(http.MethodPut, "/ems/screen/"+layerID, map[string]any{
		"layer": layerID, "version": 0, "title": title, "columns": 2, "point_ids": []string{},
		"slots": map[string]any{
			"top":  []string{"status", "kpi"},
			"main": []string{middleBlock, "devices", "recent"},
			"side": []string{"alarm", "today", "trend", "summary"},
		},
		"blocks": []any{
			map[string]any{"type": "status", "binding": map[string]any{}, "span": 2},
			map[string]any{"type": "kpi", "binding": map[string]any{"metrics": metrics}, "span": 2},
			map[string]any{"type": middleBlock, "binding": middleBinding, "span": 2},
			map[string]any{"type": "devices", "binding": map[string]any{"columns": []string{"type", "state", "value", "communication", "updated"}}, "span": 2},
			map[string]any{"type": "recent", "binding": map[string]any{"point_ids": recentIDs, "columns": []string{"value", "quality", "time"}, "rows": 5}, "span": 2},
			map[string]any{"type": "alarm", "binding": map[string]any{}, "span": 2},
			map[string]any{"type": "today", "binding": map[string]any{}, "span": 2},
			map[string]any{"type": "trend", "binding": map[string]any{"point_id": trendID}, "span": 2},
			map[string]any{"type": "summary", "binding": map[string]any{}, "span": 2},
		},
	}, nil)
}

func seedProjectRule(api *apiClient, siteID, levelID, pump1CommandID string) (string, error) {
	document := map[string]any{
		"scope": "descendants",
		"table": map[string]any{"rows": []any{
			map[string]any{
				"row_id": "low-level-start-pump1",
				"match":  "all",
				"conditions": []any{
					map[string]any{"point_id": "level", "operator": "lt", "value": 20.0, "kind": "number"},
				},
				"actions": []any{
					map[string]any{"action_id": "start_pump_1", "value": 1.0, "delay_ms": 0},
				},
			},
		}},
		"bindings": map[string]any{
			"points": map[string]any{
				"level": map[string]any{"node_id": levelID, "point_type": "value"},
			},
			"actions": map[string]any{
				"start_pump_1": map[string]any{"node_id": pump1CommandID, "point_type": "valueSet", "command_id": "start-1"},
			},
		},
	}
	var created struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	if err := api.json(http.MethodPost, "/rules", map[string]any{
		"parent": siteID, "description": projectRuleName, "document": document,
	}, &created); err != nil {
		return "", err
	}
	if created.ID == "" || created.Revision == "" {
		return "", errors.New("create rule returned no id/revision")
	}
	document["revision"] = created.Revision
	if err := api.json(http.MethodPost, "/rules/"+created.ID+"/publish", document, nil); err != nil {
		return "", err
	}
	if err := api.json(http.MethodPost, "/rules/"+created.ID+"/enable", map[string]any{"enabled": true}, nil); err != nil {
		return "", err
	}
	return created.ID, nil
}

func seedProjectMachine(api *apiClient, siteID, pump1DeviceID string) (string, error) {
	definition := map[string]any{
		"initial_state": "停止",
		"scope":         "subtree",
		"states":        []string{"停止", "运行"},
		"transitions": []any{
			map[string]any{
				"transition_id": "start", "from": "停止", "event": "启动", "to": "运行", "match": "all",
				"conditions": []any{},
				"actions":    []any{map[string]any{"action_id": "pump_start", "value": 1.0}},
			},
		},
		"bindings": map[string]any{"points": map[string]any{}, "actions": map[string]any{}},
		"command_bindings": map[string]any{
			"pump_start": map[string]any{"device_id": pump1DeviceID, "command_id": "start-1"},
		},
	}
	var created struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	if err := api.json(http.MethodPost, "/state-machines", map[string]any{
		"parent": siteID, "name": projectMachine, "definition": definition,
	}, &created); err != nil {
		return "", err
	}
	if created.ID == "" || created.Revision == 0 {
		return "", errors.New("create state machine returned no id/revision")
	}
	if err := api.json(http.MethodPost, "/state-machines/"+created.ID+"/publish", map[string]any{
		"revision": created.Revision,
	}, nil); err != nil {
		return "", err
	}
	return created.ID, nil
}

func waitForProjectValues(api *apiClient, pointIDs []string) error {
	wanted := make(map[string]bool, len(pointIDs))
	for _, id := range pointIDs {
		wanted[id] = true
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var state struct {
			Values []struct {
				PointID string `json:"point_id"`
				Quality string `json:"quality"`
				Value   struct {
					Number *float64 `json:"number"`
					Bool   *bool    `json:"bool"`
				} `json:"value"`
			} `json:"values"`
		}
		if err := api.json(http.MethodGet, "/ems/state", nil, &state); err == nil {
			good := 0
			for _, value := range state.Values {
				if wanted[value.PointID] && value.Quality == "good" && (value.Value.Number != nil || value.Value.Bool != nil) {
					good++
				}
			}
			if good == len(wanted) {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("the teaching project channels produced no fresh values; check the simulated station")
}

func publishProjectVersion(api *apiClient, siteID string) (string, error) {
	var gates struct {
		Status      string `json:"status"`
		Fingerprint string `json:"fingerprint"`
		Gates       []struct {
			Key    string `json:"key"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"gates"`
	}
	if err := api.json(http.MethodGet, "/delivery/gates?site="+siteID, nil, &gates); err != nil {
		return "", err
	}
	if gates.Status == "fail" {
		blocked := make([]string, 0, len(gates.Gates))
		for _, gate := range gates.Gates {
			if gate.Status == "fail" {
				blocked = append(blocked, gate.Key+"："+gate.Detail)
			}
		}
		return "", fmt.Errorf("preflight blocked publication: %s", strings.Join(blocked, "；"))
	}
	if gates.Fingerprint == "" {
		return "", errors.New("delivery gates returned no fingerprint")
	}
	var version struct {
		ID string `json:"id"`
	}
	if err := api.json(http.MethodPost, "/projects/publish", map[string]any{
		"site": siteID, "note": projectVersionNote, "confirm": true, "expected_fingerprint": gates.Fingerprint,
	}, &version); err != nil {
		return "", err
	}
	return version.ID, nil
}

func printProjectSummary(project projectResult, port string) {
	log.Printf("teaching project ready (three devices across the simulated station, NOT real hardware)")
	log.Printf("  open http://127.0.0.1:%s/ and sign in as admin", port)
	if project.Existing {
		log.Printf("  project already exists in this data directory; nothing was created again")
		log.Printf("  site: %s", project.Site)
	} else {
		log.Printf("  site=%s area=%s unit=%s", project.Site, project.Area, project.Unit)
		log.Printf("  devices: %s=%s %s=%s %s=%s",
			projectPump1, project.Devices[projectPump1], projectPump2, project.Devices[projectPump2],
			projectTank, project.Devices[projectTank])
		log.Printf("  live channels: %s", strings.Join(project.Channels, ", "))
		log.Printf("  site screen: GET http://127.0.0.1:%s/api/ems/screen/%s", port, project.Site)
		log.Printf("  rule=%s state-machine=%s published-version=%s", project.RuleID, project.MachineID, project.VersionID)
	}
	log.Printf("  fault injection: curl -X POST http://127.0.0.1:15021/pump/1/fault -d '{\"active\":true}'")
	log.Printf("  local mode:      curl -X POST http://127.0.0.1:15021/pump/1/local -d '{\"active\":true}'")
	log.Printf("  simulator state: curl http://127.0.0.1:15021/state")
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
