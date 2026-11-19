package appapi

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
	siotmodbus "github.com/simpleiot/simpleiot/modbus"
	"github.com/simpleiot/simpleiot/respreader"
	"go.bug.st/serial"

	"fastiot/internal/ems/pointconfig"
)

type ModbusConnectionInput struct {
	Parent      string `json:"parent"`
	Description string `json:"description"`
	Protocol    string `json:"protocol"`
	URI         string `json:"uri"`
	Port        string `json:"port"`
	Baud        string `json:"baud"`
	DataBits    int    `json:"data_bits"`
	Parity      string `json:"parity"`
	StopBits    int    `json:"stop_bits"`
	PollPeriod  int    `json:"poll_period"`
	Timeout     int    `json:"timeout"`
	Disabled    bool   `json:"disabled"`
}

func normalizeSerial(input *ModbusConnectionInput) {
	if input.Protocol != data.PointValueRTU {
		return
	}
	if input.DataBits == 0 {
		input.DataBits = 8
	}
	if input.Parity == "" {
		input.Parity = "N"
	}
	input.Parity = strings.ToUpper(input.Parity)
	if input.StopBits == 0 {
		input.StopBits = 1
	}
}

func serialPoints(input ModbusConnectionInput) data.Points {
	return data.Points{
		data.NewPointInt("dataBits", "", int64(input.DataBits)),
		data.NewPointString("parity", "", input.Parity),
		data.NewPointInt("stopBits", "", int64(input.StopBits)),
	}
}

// ModbusIOInput is the wire value for one point's configuration. The config
// module owns its encode/decode/validate/diff/CSV operations.
type ModbusIOInput = pointconfig.Point

func (h *Handler) createModbus(w http.ResponseWriter, r *http.Request, identity Identity) {
	var input ModbusConnectionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	normalizeSerial(&input)
	if input.Description == "" || input.Parent == "" || validateModbusConnection(input) != nil {
		writeError(w, http.StatusBadRequest, "invalid_modbus_connection")
		return
	}
	if input.PollPeriod == 0 {
		input.PollPeriod = 1000
	}
	if input.Timeout == 0 {
		input.Timeout = 1000
	}
	id := data.NewID()
	err := client.SendNodeType(h.nc, client.Modbus{
		ID: id, Parent: input.Parent, Description: input.Description,
		ClientServer: data.PointValueClient, Protocol: input.Protocol,
		URI: input.URI, Port: input.Port, Baud: input.Baud,
		PollPeriod: input.PollPeriod, Timeout: input.Timeout, Disabled: input.Disabled,
	}, identity.UserID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_failed")
		return
	}
	if err := client.SendNodePoints(h.nc, id, serialPoints(input), true); err != nil {
		writeError(w, http.StatusBadRequest, "create_failed")
		return
	}
	writeJSON(w, map[string]string{"id": id})
}

func (h *Handler) createModbusIO(w http.ResponseWriter, r *http.Request, identity Identity, busID string) {
	bus, ok := h.routeNode(w, busID)
	if !ok {
		return
	}
	if bus.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	var input ModbusIOInput
	input.Parent = busID
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Parent = busID
	if err := pointconfig.Validate(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	encoded := newModbusIONode(busID, input)
	if err := client.SendNode(h.nc, encoded, identity.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "create_failed")
		return
	}
	writeJSON(w, map[string]string{"id": encoded.ID})
}

func newModbusIONode(busID string, input ModbusIOInput) data.NodeEdge {
	return data.NodeEdge{
		ID: data.NewID(), Parent: busID, Type: data.NodeTypeModbusIO,
		Points: append(pointconfig.Encode(input), data.NewPointString("businessId", "", data.NewID())),
	}
}

func (h *Handler) importModbusCSV(w http.ResponseWriter, r *http.Request, identity Identity, busID string) {
	bus, ok := h.routeNode(w, busID)
	if !ok {
		return
	}
	if bus.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	content, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(content) {
		writeError(w, http.StatusBadRequest, "invalid_utf8_csv")
		return
	}
	reader := csv.NewReader(bytes.NewReader(content))
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		writeError(w, http.StatusBadRequest, "empty_or_invalid_csv")
		return
	}
	table, err := pointconfig.NewTable(records[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := r.URL.Query()
	diffMode := query.Get("diff") == "true" || query.Get("apply") == "diff"
	existing := map[string]bool{}
	if !diffMode {
		children, _ := client.GetNodes(h.nc, busID, "all", "", false)
		for _, child := range children {
			name, _ := child.Points.Text(data.PointTypeDescription, "")
			existing[name] = true
		}
	}

	inputs := []ModbusIOInput{}
	for rowIndex, record := range records[1:] {
		blank := true
		for _, cell := range record {
			if strings.TrimSpace(cell) != "" {
				blank = false
			}
		}
		if blank {
			continue
		}
		input, err := table.Point(record, rowIndex+2)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		input.Parent = busID
		if existing[input.Description] {
			writeError(w, http.StatusConflict, "duplicate_point_name: "+input.Description)
			return
		}
		existing[input.Description] = true
		inputs = append(inputs, input)
	}
	if len(inputs) == 0 {
		writeError(w, http.StatusBadRequest, "empty_or_invalid_csv")
		return
	}
	if diffMode {
		h.modbusCSVDiff(w, r, identity, busID, inputs)
		return
	}
	if r.URL.Query().Get("preview") == "true" {
		writeJSON(w, map[string]any{"preview": true, "count": len(inputs), "points": inputs})
		return
	}

	if err := h.createModbusBatch(busID, inputs, identity.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "import_rolled_back")
		return
	}
	writeJSON(w, map[string]int{"imported": len(inputs)})
}

func (h *Handler) createModbusBatch(busID string, inputs []ModbusIOInput, origin string) error {
	created := []data.NodeEdge{}
	for _, input := range inputs {
		node := newModbusIONode(busID, input)
		if err := client.SendNode(h.nc, node, origin); err != nil {
			for _, rollback := range created {
				_ = client.DeleteNode(h.nc, rollback.ID, busID, origin)
			}
			return err
		}
		created = append(created, node)
	}
	return nil
}

type modbusDiffChange struct {
	Name   string        `json:"name"`
	Before ModbusIOInput `json:"before"`
	After  ModbusIOInput `json:"after"`
	Fields []string      `json:"fields"`
}

type existingModbusIO struct {
	NodeID string
	Input  ModbusIOInput
}

func (h *Handler) existingModbusIOs(busID string) ([]existingModbusIO, error) {
	children, err := client.GetNodes(h.nc, busID, "all", "", false)
	if err != nil {
		return nil, err
	}
	items := []existingModbusIO{}
	for _, node := range children {
		if node.Type != data.NodeTypeModbusIO {
			continue
		}
		item := pointconfig.Decode(node)
		item.Parent = busID
		items = append(items, existingModbusIO{NodeID: node.ID, Input: item})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Input.Address != items[j].Input.Address {
			return items[i].Input.Address < items[j].Input.Address
		}
		return items[i].Input.Description < items[j].Input.Description
	})
	return items, nil
}

// modbusCSVDiff compares a validated CSV against the channel's current point
// table. Names identify points; apply=diff creates/updates the differences and
// only deletes missing points when remove=true is passed explicitly.
func (h *Handler) modbusCSVDiff(w http.ResponseWriter, r *http.Request, identity Identity, busID string, inputs []ModbusIOInput) {
	existing, err := h.existingModbusIOs(busID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "diff_unavailable")
		return
	}
	byName := map[string]existingModbusIO{}
	for _, item := range existing {
		if _, duplicate := byName[item.Input.Description]; duplicate {
			writeError(w, http.StatusConflict, "duplicate_existing_point_name: "+item.Input.Description)
			return
		}
		byName[item.Input.Description] = item
	}
	added := []ModbusIOInput{}
	changed := []modbusDiffChange{}
	removedItems := []existingModbusIO{}
	unchanged := 0
	inCSV := map[string]bool{}
	for i := range inputs {
		input := inputs[i]
		inCSV[input.Description] = true
		current, ok := byName[input.Description]
		if !ok {
			added = append(added, input)
			continue
		}
		fields := pointconfig.Diff(current.Input, input)
		if len(fields) == 0 {
			unchanged++
			continue
		}
		changed = append(changed, modbusDiffChange{Name: input.Description, Before: current.Input, After: input, Fields: fields})
	}
	for _, item := range existing {
		if !inCSV[item.Input.Description] {
			removedItems = append(removedItems, item)
		}
	}
	removed := make([]ModbusIOInput, 0, len(removedItems))
	for _, item := range removedItems {
		removed = append(removed, item.Input)
	}
	if r.URL.Query().Get("apply") != "diff" {
		writeJSON(w, map[string]any{
			"diff": true, "added": added, "changed": changed,
			"removed": removed, "unchanged": unchanged,
		})
		return
	}
	if err := h.createModbusBatch(busID, added, identity.UserID); err != nil {
		writeError(w, http.StatusInternalServerError, "import_rolled_back")
		return
	}
	for _, change := range changed {
		if err := h.updateModbusIOFields(byName[change.Name].NodeID, change.After, identity.UserID); err != nil {
			writeError(w, http.StatusInternalServerError, "sync_update_failed: "+change.Name)
			return
		}
	}
	removedCount := 0
	if r.URL.Query().Get("remove") == "true" {
		for _, item := range removedItems {
			if err := client.DeleteNode(h.nc, item.NodeID, busID, identity.UserID); err != nil {
				writeError(w, http.StatusInternalServerError, "sync_delete_failed: "+item.Input.Description)
				return
			}
			removedCount++
		}
	}
	writeJSON(w, map[string]any{"applied": true, "added": len(added), "updated": len(changed), "removed": removedCount})
}

func (h *Handler) testModbus(w http.ResponseWriter, r *http.Request, identity Identity) {
	var input struct {
		ModbusConnectionInput
		ServerID int    `json:"server_id"`
		Address  int    `json:"address"`
		IOType   string `json:"io_type"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	normalizeSerial(&input.ModbusConnectionInput)
	if err := validateModbusConnection(input.ModbusConnectionInput); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.ServerID < 1 || input.ServerID > 247 || input.Address < 0 || input.Address > 65535 {
		writeError(w, http.StatusBadRequest, "invalid_modbus_address")
		return
	}
	value, err := readModbus(input.ModbusConnectionInput, input.ServerID, input.Address, input.IOType)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read_failed: "+err.Error())
		return
	}
	writeJSON(w, map[string]any{"success": true, "value": value})
}

func (h *Handler) updateModbus(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	if node.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	var input ModbusConnectionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	normalizeSerial(&input)
	if input.Description == "" || validateModbusConnection(input) != nil {
		writeError(w, http.StatusBadRequest, "invalid_modbus_connection")
		return
	}
	if input.PollPeriod == 0 {
		input.PollPeriod = 1000
	}
	if input.Timeout == 0 {
		input.Timeout = 1000
	}
	encoded, err := data.Encode(client.Modbus{
		ID: id, Parent: node.Parent, Description: input.Description,
		ClientServer: data.PointValueClient, Protocol: input.Protocol,
		URI: input.URI, Port: input.Port, Baud: input.Baud,
		PollPeriod: input.PollPeriod, Timeout: input.Timeout, Disabled: input.Disabled,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_modbus_connection")
		return
	}
	encoded.Points = append(encoded.Points, serialPoints(input)...)
	for i := range encoded.Points {
		encoded.Points[i].Origin = identity.UserID
	}
	defer h.invalidatePhysicalCalculations(id)
	if err := client.SendNodePoints(h.nc, id, encoded.Points, true); err != nil {
		writeError(w, http.StatusBadRequest, "save_failed")
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}

func (h *Handler) updateModbusIO(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	node, ok := h.routeNode(w, id)
	if !ok {
		return
	}
	if node.Type != data.NodeTypeModbusIO {
		writeError(w, http.StatusBadRequest, "invalid_modbus_point")
		return
	}
	var input ModbusIOInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := pointconfig.Validate(input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.updateModbusIOFields(id, input, identity.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "save_failed")
		return
	}
	writeJSON(w, map[string]bool{"saved": true})
}

func (h *Handler) updateModbusIOFields(id string, input ModbusIOInput, origin string) error {
	defer h.CalculationRuntime().Invalidate([]string{id})
	points := pointconfig.Encode(input)
	for i := range points {
		points[i].Origin = origin
	}
	return client.SendNodePoints(h.nc, id, points, true)
}

func (h *Handler) exportModbusCSV(w http.ResponseWriter, identity Identity, busID string) {
	bus, ok := h.routeNode(w, busID)
	if !ok {
		return
	}
	if bus.Type != data.NodeTypeModbus {
		writeError(w, http.StatusBadRequest, "invalid_modbus_bus")
		return
	}
	children, err := client.GetNodes(h.nc, busID, "all", "", false)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "export_failed")
		return
	}
	sort.Slice(children, func(i, j int) bool {
		left, _ := children[i].Points.Value(data.PointTypeAddress, "")
		right, _ := children[j].Points.Value(data.PointTypeAddress, "")
		if left != right {
			return left < right
		}
		return children[i].ID < children[j].ID
	})
	var buffer bytes.Buffer
	buffer.WriteString("\xef\xbb\xbf")
	writer := csv.NewWriter(&buffer)
	_ = writer.Write(pointconfig.Columns)
	for _, node := range children {
		_ = writer.Write(pointconfig.Row(pointconfig.Decode(node)))
	}
	writer.Flush()
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="modbus-points.csv"`)
	_, _ = w.Write(buffer.Bytes())
}

func validateModbusConnection(input ModbusConnectionInput) error {
	switch input.Protocol {
	case data.PointValueTCP:
		host, port, err := net.SplitHostPort(input.URI)
		if err != nil || host == "" {
			return fmt.Errorf("TCP address must be host:port")
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return fmt.Errorf("invalid TCP port")
		}
	case data.PointValueRTU:
		baud, err := strconv.Atoi(input.Baud)
		if input.Port == "" || err != nil || baud < 1 || baud > 4_000_000 {
			return fmt.Errorf("invalid RTU port or baud")
		}
		if input.DataBits != 0 && (input.DataBits < 5 || input.DataBits > 8) {
			return fmt.Errorf("invalid data bits")
		}
		switch strings.ToUpper(input.Parity) {
		case "", "N", "E", "O":
		default:
			return fmt.Errorf("invalid parity")
		}
		if input.StopBits != 0 && input.StopBits != 1 && input.StopBits != 2 {
			return fmt.Errorf("invalid stop bits")
		}
	default:
		return fmt.Errorf("protocol must be TCP or RTU")
	}
	if input.PollPeriod < 0 || input.PollPeriod > 60_000 || input.Timeout < 0 || input.Timeout > 10_000 {
		return fmt.Errorf("invalid timing")
	}
	return nil
}

func openModbusClient(connection ModbusConnectionInput) (*siotmodbus.Client, func() error, error) {
	if connection.Protocol == data.PointValueTCP {
		socket, err := net.DialTimeout("tcp", connection.URI, 3*time.Second)
		if err != nil {
			return nil, nil, err
		}
		return siotmodbus.NewClient(siotmodbus.NewTCP(socket, 2*time.Second, siotmodbus.TransportClient), 0), socket.Close, nil
	}
	baud, _ := strconv.Atoi(connection.Baud)
	mode := &serial.Mode{BaudRate: baud, DataBits: 8, Parity: serial.NoParity, StopBits: serial.OneStopBit}
	if connection.DataBits >= 5 && connection.DataBits <= 8 {
		mode.DataBits = connection.DataBits
	}
	switch strings.ToUpper(connection.Parity) {
	case "E":
		mode.Parity = serial.EvenParity
	case "O":
		mode.Parity = serial.OddParity
	}
	if connection.StopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	}
	port, err := serial.Open(connection.Port, mode)
	if err != nil {
		return nil, nil, err
	}
	reader := respreader.NewReadWriteCloser(port, 2*time.Second, 20*time.Millisecond)
	return siotmodbus.NewClient(siotmodbus.NewRTU(reader), 0), port.Close, nil
}

func readModbus(connection ModbusConnectionInput, serverID, address int, ioType string) (any, error) {
	clientConnection, close, err := openModbusClient(connection)
	if err != nil {
		return nil, err
	}
	defer close()
	switch ioType {
	case data.PointValueModbusCoil:
		return clientConnection.ReadCoils(byte(serverID), uint16(address), 1)
	case data.PointValueModbusDiscreteInput:
		return clientConnection.ReadDiscreteInputs(byte(serverID), uint16(address), 1)
	case data.PointValueModbusHoldingRegister:
		return clientConnection.ReadHoldingRegs(byte(serverID), uint16(address), 1)
	case data.PointValueModbusInputRegister:
		return clientConnection.ReadInputRegs(byte(serverID), uint16(address), 1)
	default:
		return nil, fmt.Errorf("unsupported read type")
	}
}
