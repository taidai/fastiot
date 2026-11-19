package pointconfig

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

func sample() Point {
	return Point{
		Parent: "bus", Description: "Flow", ServerID: 3, Address: 40,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: data.PointValueINT16,
		ReadOnly: false, Scale: 0.5, Offset: 1.5, Units: "m3/h",
	}
}

func TestIOTypesHaveOneVocabulary(t *testing.T) {
	for _, ioType := range []string{
		data.PointValueModbusHoldingRegister, data.PointValueModbusInputRegister,
		data.PointValueModbusCoil, data.PointValueModbusDiscreteInput,
	} {
		label := TypeLabel(ioType)
		if label == "" {
			t.Fatalf("%s has no label", ioType)
		}
		if got := TypeValue(label); got != ioType {
			t.Errorf("TypeValue(%q) = %q, want %q", label, got, ioType)
		}
	}
	if TypeLabel("unknown") != "" {
		t.Error("unknown io type must not invent a label")
	}
}

func TestCSVRowRoundTripsEveryField(t *testing.T) {
	point := sample()
	record := Row(point)
	if len(record) != len(Columns) {
		t.Fatalf("row has %d fields, header has %d", len(record), len(Columns))
	}
	if got := record[0]; got != point.Description {
		t.Errorf("name = %q", got)
	}
	if got := record[3]; got != "保持寄存器" {
		t.Errorf("type label = %q", got)
	}
	table, err := NewTable(append([]string(nil), Columns...))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := table.Point(record, 2)
	if err != nil {
		t.Fatal(err)
	}
	point.Parent = ""
	if !reflect.DeepEqual(parsed, point) {
		t.Errorf("round trip = %+v, want %+v", parsed, point)
	}
}

func TestEmptyScaleIsTheDefaultOne(t *testing.T) {
	table, err := NewTable(append([]string(nil), Columns...))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := table.Point([]string{"P", "1", "10", "保持寄存器", "uint16", "否", "", "", ""}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scale != 1 || parsed.DataFormat != data.PointValueUINT16 {
		t.Errorf("defaults not applied: %+v", parsed)
	}
	if parsed.Offset != 0 {
		t.Errorf("empty offset = %v, want 0", parsed.Offset)
	}
}

func TestTableRejectsBadHeaders(t *testing.T) {
	if _, err := NewTable([]string{"名称", "名称", "地址", "类型"}); err == nil || err.Error() != "duplicate_csv_column" {
		t.Errorf("duplicate header error = %v", err)
	}
	if _, err := NewTable([]string{"名称", "站号", "地址"}); err == nil || !strings.Contains(err.Error(), "missing_csv_column") {
		t.Errorf("missing header error = %v", err)
	}
}

func TestRowErrorsNameTheLineAndField(t *testing.T) {
	table, err := NewTable(append([]string(nil), Columns...))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := table.Point([]string{"P", "1", "10"}, 4); err == nil || err.Error() != "第 4 行字段数量不正确" {
		t.Errorf("field count error = %v", err)
	}
	if _, err := table.Point([]string{"P", "x", "10", "保持寄存器", "uint16", "否", "1", "0", ""}, 5); err == nil || err.Error() != "第 5 行站号或地址无效" {
		t.Errorf("address error = %v", err)
	}
	if _, err := table.Point([]string{"P", "1", "10", "保持寄存器", "uint16", "maybe", "1", "0", ""}, 6); err == nil || err.Error() != "第 6 行只读字段无效" {
		t.Errorf("read-only error = %v", err)
	}
	if _, err := table.Point([]string{"P", "1", "10", "保持寄存器", "uint16", "否", "x", "0", ""}, 7); err == nil || err.Error() != "第 7 行缩放无效" {
		t.Errorf("scale error = %v", err)
	}
	if _, err := table.Point([]string{"P", "1", "10", "保持寄存器", "uint16", "否", "1", "x", ""}, 8); err == nil || err.Error() != "第 8 行偏移无效" {
		t.Errorf("offset error = %v", err)
	}
	if _, err := table.Point([]string{"P", "1", "10", "保持寄存器", "float64", "否", "1", "0", ""}, 9); err == nil || err.Error() != "第 9 行: invalid_modbus_data_format" {
		t.Errorf("validation error = %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Point)
		want   string
	}{
		{"no name", func(p *Point) { p.Description = "" }, "invalid_modbus_address"},
		{"bad server", func(p *Point) { p.ServerID = 0 }, "invalid_modbus_address"},
		{"bad address", func(p *Point) { p.Address = 70000 }, "invalid_modbus_address"},
		{"nan scale", func(p *Point) { p.Scale = math.NaN() }, "invalid_scaling"},
		{"input point writable", func(p *Point) { p.IOType = data.PointValueModbusInputRegister; p.ReadOnly = false }, "input_points_must_be_read_only"},
		{"unknown type", func(p *Point) { p.IOType = "reg" }, "invalid_modbus_io_type"},
		{"unknown format", func(p *Point) { p.DataFormat = "float64" }, "invalid_modbus_data_format"},
		{"register pair out of range", func(p *Point) { p.DataFormat = data.PointValueFLOAT32; p.Address = 65535 }, "register_pair_out_of_range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			point := sample()
			tc.mutate(&point)
			if err := Validate(point); err == nil || err.Error() != tc.want {
				t.Errorf("Validate = %v, want %s", err, tc.want)
			}
		})
	}
	if err := Validate(sample()); err != nil {
		t.Errorf("sample must validate: %v", err)
	}
}

func TestDiffNamesOneChangedColumn(t *testing.T) {
	base := sample()
	cases := []struct {
		column string
		mutate func(*Point)
	}{
		{"站号", func(p *Point) { p.ServerID = 4 }},
		{"地址", func(p *Point) { p.Address = 41 }},
		{"类型", func(p *Point) { p.IOType = data.PointValueModbusInputRegister }},
		{"数据格式", func(p *Point) { p.DataFormat = data.PointValueUINT32 }},
		{"只读", func(p *Point) { p.ReadOnly = true }},
		{"缩放", func(p *Point) { p.Scale = 2 }},
		{"偏移", func(p *Point) { p.Offset = 2 }},
		{"单位", func(p *Point) { p.Units = "L/min" }},
	}
	for _, tc := range cases {
		t.Run(tc.column, func(t *testing.T) {
			after := base
			tc.mutate(&after)
			if fields := Diff(base, after); len(fields) != 1 || fields[0] != tc.column {
				t.Errorf("Diff = %v, want [%s]", fields, tc.column)
			}
		})
	}
	if fields := Diff(base, base); len(fields) != 0 {
		t.Errorf("identical points differ: %v", fields)
	}
	// Data format is not meaningful for bit areas.
	coil := base
	coil.IOType = data.PointValueModbusCoil
	other := coil
	other.DataFormat = data.PointValueFLOAT32
	if fields := Diff(coil, other); len(fields) != 0 {
		t.Errorf("coil data format must not be compared: %v", fields)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	point := sample()
	node := data.NodeEdge{ID: "io", Parent: "bus", Type: data.NodeTypeModbusIO, Points: storeShape(Encode(point))}
	decoded := Decode(node)
	point.Parent = ""
	if !reflect.DeepEqual(decoded, point) {
		t.Errorf("decode = %+v, want %+v", decoded, point)
	}
}

func TestEncodeNeverWritesZeroScale(t *testing.T) {
	point := sample()
	point.Scale = 0
	node := data.NodeEdge{Parent: "bus", Points: storeShape(Encode(point))}
	if scale, _ := node.Points.Value(data.PointTypeScale, ""); scale != 1 {
		t.Errorf("encoded scale = %v, want 1", scale)
	}
}

// storeShape mirrors how the node store key an unkeyed point: an empty key
// becomes "0", which is also what Find looks up.
func storeShape(points data.Points) data.Points {
	for i := range points {
		if points[i].Key == "" {
			points[i].Key = "0"
		}
	}
	return points
}
