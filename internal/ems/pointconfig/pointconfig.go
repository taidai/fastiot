// Package pointconfig owns the configuration value of one Modbus point and the
// operations that read, write, validate, compare and round-trip it through CSV.
// Runtime observations (value/valueSet, health, poll counters) are deliberately
// not part of this value: saving configuration must never look like a fresh
// observation.
package pointconfig

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/simpleiot/simpleiot/data"
)

// Point is the writable configuration of one Modbus point.
type Point struct {
	Parent      string  `json:"parent"`
	Description string  `json:"description"`
	ServerID    int     `json:"server_id"`
	Address     int     `json:"address"`
	IOType      string  `json:"io_type"`
	DataFormat  string  `json:"data_format"`
	ReadOnly    bool    `json:"read_only"`
	Scale       float64 `json:"scale"`
	Offset      float64 `json:"offset"`
	Units       string  `json:"units"`
	Disabled    bool    `json:"disabled"`
}

// columnID names one CSV field so every consumer refers to the field instead
// of repeating its Chinese header.
type columnID int

const (
	colName columnID = iota
	colServerID
	colAddress
	colIOType
	colDataFormat
	colReadOnly
	colScale
	colOffset
	colUnits
)

// columnOrder is the only definition of the CSV vocabulary: header names,
// export order and how each field is written all live here.
var columnOrder = []struct {
	id    columnID
	name  string
	value func(Point) string
}{
	{colName, "名称", func(p Point) string { return p.Description }},
	{colServerID, "站号", func(p Point) string { return strconv.Itoa(p.ServerID) }},
	{colAddress, "地址", func(p Point) string { return strconv.Itoa(p.Address) }},
	{colIOType, "类型", func(p Point) string { return TypeLabel(p.IOType) }},
	{colDataFormat, "数据格式", func(p Point) string { return p.DataFormat }},
	{colReadOnly, "只读", func(p Point) string { return boolLabel(p.ReadOnly) }},
	{colScale, "缩放", func(p Point) string { return strconv.FormatFloat(p.Scale, 'g', -1, 64) }},
	{colOffset, "偏移", func(p Point) string { return strconv.FormatFloat(p.Offset, 'g', -1, 64) }},
	{colUnits, "单位", func(p Point) string { return p.Units }},
}

func columnName(id columnID) string {
	for _, definition := range columnOrder {
		if definition.id == id {
			return definition.name
		}
	}
	return ""
}

func columnNames() []string {
	names := make([]string, len(columnOrder))
	for index, definition := range columnOrder {
		names[index] = definition.name
	}
	return names
}

// Columns is the CSV column vocabulary. Its order is the export order.
var Columns = columnNames()

// The IO type names are the only place the CSV/UI vocabulary is defined.
var ioTypes = []struct {
	value string
	label string
}{
	{data.PointValueModbusHoldingRegister, "保持寄存器"},
	{data.PointValueModbusInputRegister, "输入寄存器"},
	{data.PointValueModbusCoil, "线圈"},
	{data.PointValueModbusDiscreteInput, "离散输入"},
}

// TypeLabel returns the display/CSV name for an IO type, or "" when unknown.
func TypeLabel(ioType string) string {
	for _, entry := range ioTypes {
		if entry.value == ioType {
			return entry.label
		}
	}
	return ""
}

// TypeValue maps a CSV/UI name to its IO type. A value that is already a
// canonical IO type is returned unchanged so callers can accept both forms.
func TypeValue(text string) string {
	for _, entry := range ioTypes {
		if entry.label == text {
			return entry.value
		}
	}
	return text
}

// IsRegister reports whether the IO type is a 16-bit word area that needs a
// data format.
func IsRegister(ioType string) bool {
	return ioType == data.PointValueModbusHoldingRegister || ioType == data.PointValueModbusInputRegister
}

// IsReadOnlyType reports whether the IO type can only be read.
func IsReadOnlyType(ioType string) bool {
	return ioType == data.PointValueModbusInputRegister || ioType == data.PointValueModbusDiscreteInput
}

// ParseBool reads the CSV boolean vocabulary.
func ParseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "是", "true", "1", "yes":
		return true, nil
	case "否", "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean")
	}
}

func boolLabel(value bool) string {
	if value {
		return "是"
	}
	return "否"
}

// Scale 0 is not a meaningful scale; every read path treats it as 1 so a field
// saved through create, update or CSV has one stored value.
func (p Point) normalized() Point {
	if p.Scale == 0 {
		p.Scale = 1
	}
	return p
}

// Validate rejects configurations the runtime cannot poll.
func Validate(point Point) error {
	if point.Description == "" || point.ServerID < 1 || point.ServerID > 247 || point.Address < 0 || point.Address > 65535 {
		return fmt.Errorf("invalid_modbus_address")
	}
	if !finite(point.Scale) || !finite(point.Offset) {
		return fmt.Errorf("invalid_scaling")
	}
	switch point.IOType {
	case data.PointValueModbusDiscreteInput, data.PointValueModbusInputRegister:
		if !point.ReadOnly {
			return fmt.Errorf("input_points_must_be_read_only")
		}
	case data.PointValueModbusCoil, data.PointValueModbusHoldingRegister:
	default:
		return fmt.Errorf("invalid_modbus_io_type")
	}
	if !IsRegister(point.IOType) {
		return nil
	}
	switch point.DataFormat {
	case data.PointValueUINT16, data.PointValueINT16:
	case data.PointValueUINT32, data.PointValueINT32, data.PointValueFLOAT32:
		if point.Address > 65534 {
			return fmt.Errorf("register_pair_out_of_range")
		}
	default:
		return fmt.Errorf("invalid_modbus_data_format")
	}
	return nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

// Encode lists only editable configuration. Encoding the runtime point type
// would also publish zero value/valueSet and reset runtime counters.
func Encode(point Point) data.Points {
	point = point.normalized()
	return data.Points{
		data.NewPointString(data.PointTypeDescription, "", point.Description),
		data.NewPointFloat(data.PointTypeID, "", float64(point.ServerID)),
		data.NewPointFloat(data.PointTypeAddress, "", float64(point.Address)),
		data.NewPointString(data.PointTypeModbusIOType, "", point.IOType),
		data.NewPointString(data.PointTypeDataFormat, "", point.DataFormat),
		data.NewPointFloat(data.PointTypeReadOnly, "", data.BoolToFloat(point.ReadOnly)),
		data.NewPointFloat(data.PointTypeScale, "", point.Scale),
		data.NewPointFloat(data.PointTypeOffset, "", point.Offset),
		data.NewPointFloat(data.PointTypeDisabled, "", data.BoolToFloat(point.Disabled)),
		data.NewPointString(data.PointTypeUnits, "", point.Units),
	}
}

// Decode reads the configuration back from a point node. The parent is node
// topology, not point configuration, so it is left empty: a copied template
// must not carry the source bus ID.
func Decode(node data.NodeEdge) Point {
	serverID, _ := node.Points.Value(data.PointTypeID, "")
	address, _ := node.Points.Value(data.PointTypeAddress, "")
	scale, _ := node.Points.Value(data.PointTypeScale, "")
	offset, _ := node.Points.Value(data.PointTypeOffset, "")
	readOnly, _ := node.Points.Value(data.PointTypeReadOnly, "")
	disabled, _ := node.Points.Value(data.PointTypeDisabled, "")
	description, _ := node.Points.Text(data.PointTypeDescription, "")
	ioType, _ := node.Points.Text(data.PointTypeModbusIOType, "")
	format, _ := node.Points.Text(data.PointTypeDataFormat, "")
	units, _ := node.Points.Text(data.PointTypeUnits, "")
	return Point{
		Description: description,
		ServerID:    int(serverID), Address: int(address),
		IOType: ioType, DataFormat: format, ReadOnly: readOnly != 0,
		Scale: scale, Offset: offset, Units: units, Disabled: disabled != 0,
	}.normalized()
}

// Diff names the CSV columns whose value changed. Names identify points, so the
// name itself is never reported.
func Diff(before, after Point) []string {
	before, after = before.normalized(), after.normalized()
	fields := []string{}
	if before.ServerID != after.ServerID {
		fields = append(fields, columnName(colServerID))
	}
	if before.Address != after.Address {
		fields = append(fields, columnName(colAddress))
	}
	if before.IOType != after.IOType {
		fields = append(fields, columnName(colIOType))
	}
	if IsRegister(after.IOType) && before.DataFormat != after.DataFormat {
		fields = append(fields, columnName(colDataFormat))
	}
	if before.ReadOnly != after.ReadOnly {
		fields = append(fields, columnName(colReadOnly))
	}
	if before.Scale != after.Scale {
		fields = append(fields, columnName(colScale))
	}
	if before.Offset != after.Offset {
		fields = append(fields, columnName(colOffset))
	}
	if strings.TrimSpace(before.Units) != strings.TrimSpace(after.Units) {
		fields = append(fields, columnName(colUnits))
	}
	return fields
}

// Row formats one point as a CSV record in Columns order.
func Row(point Point) []string {
	point = point.normalized()
	record := make([]string, len(columnOrder))
	for index, definition := range columnOrder {
		record[index] = definition.value(point)
	}
	return record
}

// Table is a parsed CSV header. It maps column names to indexes once so every
// row is read through the same vocabulary.
type Table struct {
	columns map[string]int
	width   int
}

// NewTable validates the required columns and rejects duplicates.
func NewTable(header []string) (Table, error) {
	columns := map[string]int{}
	for index, name := range header {
		name = strings.TrimSpace(strings.TrimPrefix(name, "\xef\xbb\xbf"))
		if _, exists := columns[name]; exists {
			return Table{}, fmt.Errorf("duplicate_csv_column")
		}
		columns[name] = index
	}
	for _, required := range columnOrder[:4] {
		if _, ok := columns[required.name]; !ok {
			return Table{}, fmt.Errorf("missing_csv_column: %s", required.name)
		}
	}
	return Table{columns: columns, width: len(header)}, nil
}

// Point parses one CSV record. line is the 1-based line number shown to the
// operator. The returned point has no parent: the caller binds it to a bus.
func (t Table) Point(record []string, line int) (Point, error) {
	if len(record) != t.width {
		return Point{}, fmt.Errorf("第 %d 行字段数量不正确", line)
	}
	cell := func(id columnID) string {
		index, ok := t.columns[columnName(id)]
		if !ok {
			return ""
		}
		return strings.TrimSpace(record[index])
	}
	serverID, serverErr := strconv.Atoi(cell(colServerID))
	address, addressErr := strconv.Atoi(cell(colAddress))
	if serverErr != nil || addressErr != nil {
		return Point{}, fmt.Errorf("第 %d 行站号或地址无效", line)
	}
	ioType := TypeValue(cell(colIOType))
	readOnly := IsReadOnlyType(ioType)
	if text := cell(colReadOnly); text != "" {
		value, err := ParseBool(text)
		if err != nil {
			return Point{}, fmt.Errorf("第 %d 行只读字段无效", line)
		}
		readOnly = value
	}
	scale := 1.0
	if text := cell(colScale); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Point{}, fmt.Errorf("第 %d 行缩放无效", line)
		}
		scale = value
	}
	offset := 0.0
	if text := cell(colOffset); text != "" {
		value, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return Point{}, fmt.Errorf("第 %d 行偏移无效", line)
		}
		offset = value
	}
	format := cell(colDataFormat)
	if format == "" {
		format = data.PointValueUINT16
	}
	point := Point{
		Description: cell(colName), ServerID: serverID, Address: address,
		IOType: ioType, DataFormat: format, ReadOnly: readOnly,
		Scale: scale, Offset: offset, Units: cell(colUnits),
	}.normalized()
	if err := Validate(point); err != nil {
		return Point{}, fmt.Errorf("第 %d 行: %s", line, err)
	}
	return point, nil
}
