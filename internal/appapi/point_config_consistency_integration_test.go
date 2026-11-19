package appapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/simpleiot/simpleiot/data"
)

// One point configuration must read and write the same field the same way
// through create, update, CSV export/import and validation. Each step changes
// exactly one field and asserts that the CSV diff reports exactly that field.
func TestPointConfigurationFieldsAgreeAcrossCreateUpdateCSVAndValidate(t *testing.T) {
	f := newPointConfigFixture(t)
	busID := f.createBus(t, "Field consistency")
	input := ModbusIOInput{
		Description: "Flow", ServerID: 3, Address: 40,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: data.PointValueINT16,
		ReadOnly: false, Scale: 0.5, Offset: 1.5, Units: "m3/h",
	}
	pointID := f.createPoint(t, busID, input)
	previousCSV := f.exportCSV(t, busID)
	if want := "Flow,3,40,保持寄存器,int16,否,0.5,1.5,m3/h"; !strings.Contains(previousCSV, want) {
		t.Fatalf("export does not match create: want row %q in %q", want, previousCSV)
	}
	if fields := f.diffFields(t, busID, previousCSV); len(fields) != 0 {
		t.Fatalf("re-importing the exported point reported changes: %v", fields)
	}

	steps := []struct {
		column string
		mutate func(*ModbusIOInput)
		row    string
	}{
		{"站号", func(p *ModbusIOInput) { p.ServerID = 4 }, "Flow,4,40,保持寄存器,int16,否,0.5,1.5,m3/h"},
		{"地址", func(p *ModbusIOInput) { p.Address = 41 }, "Flow,4,41,保持寄存器,int16,否,0.5,1.5,m3/h"},
		{"缩放", func(p *ModbusIOInput) { p.Scale = 0.25 }, "Flow,4,41,保持寄存器,int16,否,0.25,1.5,m3/h"},
		{"偏移", func(p *ModbusIOInput) { p.Offset = 2 }, "Flow,4,41,保持寄存器,int16,否,0.25,2,m3/h"},
		{"单位", func(p *ModbusIOInput) { p.Units = "L/min" }, "Flow,4,41,保持寄存器,int16,否,0.25,2,L/min"},
		{"只读", func(p *ModbusIOInput) { p.ReadOnly = true }, "Flow,4,41,保持寄存器,int16,是,0.25,2,L/min"},
		{"数据格式", func(p *ModbusIOInput) { p.DataFormat = data.PointValueUINT32 }, "Flow,4,41,保持寄存器,uint32,是,0.25,2,L/min"},
		{"类型", func(p *ModbusIOInput) { p.IOType = data.PointValueModbusInputRegister }, "Flow,4,41,输入寄存器,uint32,是,0.25,2,L/min"},
		{"类型", func(p *ModbusIOInput) { p.IOType = data.PointValueModbusCoil }, "Flow,4,41,线圈,uint32,是,0.25,2,L/min"},
	}
	for _, step := range steps {
		step.mutate(&input)
		status, body := call(t, http.MethodPut, f.base+"/modbus/io/"+pointID, f.token, input, nil)
		if status != http.StatusOK {
			t.Fatalf("update %s: %d %s", step.column, status, body)
		}
		currentCSV := f.exportCSV(t, busID)
		if !strings.Contains(currentCSV, step.row) {
			t.Fatalf("after updating %s export row missing: want %q in %q", step.column, step.row, currentCSV)
		}
		fields := f.diffFields(t, busID, previousCSV)
		if len(fields) != 1 || fields[0] != step.column {
			t.Fatalf("updating %s made the CSV diff report %v", step.column, fields)
		}
		previousCSV = currentCSV
	}
}

func TestPointValidationIsConsistentAcrossCreateUpdateAndCSV(t *testing.T) {
	f := newPointConfigFixture(t)
	busID := f.createBus(t, "Validation consistency")
	valid := ModbusIOInput{
		Description: "Valve", ServerID: 1, Address: 10,
		IOType: data.PointValueModbusHoldingRegister, DataFormat: data.PointValueUINT16, ReadOnly: false, Scale: 1,
	}
	pointID := f.createPoint(t, busID, valid)

	cases := []struct {
		name   string
		reason string
		mutate func(*ModbusIOInput)
		csv    string
	}{
		{
			name:   "input point must be read only",
			reason: "input_points_must_be_read_only",
			mutate: func(p *ModbusIOInput) { p.IOType = data.PointValueModbusInputRegister; p.ReadOnly = false },
			csv:    "Valve,1,10,输入寄存器,uint16,否,1,0,\n",
		},
		{
			name:   "unknown data format",
			reason: "invalid_modbus_data_format",
			mutate: func(p *ModbusIOInput) { p.DataFormat = "float64" },
			csv:    "Valve,1,10,保持寄存器,float64,否,1,0,\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			invalid := valid
			tc.mutate(&invalid)
			status, body := call(t, http.MethodPost, f.base+"/modbus/"+busID+"/io", f.token, invalid, nil)
			if status != http.StatusBadRequest || !strings.Contains(body, tc.reason) {
				t.Errorf("create: %d %s", status, body)
			}
			status, body = call(t, http.MethodPut, f.base+"/modbus/io/"+pointID, f.token, invalid, nil)
			if status != http.StatusBadRequest || !strings.Contains(body, tc.reason) {
				t.Errorf("update: %d %s", status, body)
			}
			csv := "名称,站号,地址,类型,数据格式,只读,缩放,偏移,单位\n" + tc.csv
			for _, query := range []string{"?preview=true", "?apply=diff"} {
				status, body = callRaw(t, f.base+"/modbus/"+busID+"/io/import"+query, f.token, csv)
				if status != http.StatusBadRequest || !strings.Contains(body, tc.reason) {
					t.Errorf("CSV import%s: %d %s", query, status, body)
				}
			}
		})
	}
}

func (f *pointConfigFixture) exportCSV(t *testing.T, busID string) string {
	t.Helper()
	status, body := call(t, http.MethodGet, f.base+"/modbus/"+busID+"/io/export", f.token, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, body)
	}
	return body
}

func (f *pointConfigFixture) diffFields(t *testing.T, busID, csv string) []string {
	t.Helper()
	status, raw := callRaw(t, f.base+"/modbus/"+busID+"/io/import?diff=true", f.token, csv)
	if status != http.StatusOK {
		t.Fatalf("diff: %d %s", status, raw)
	}
	var diff struct {
		Added   []json.RawMessage `json:"added"`
		Removed []json.RawMessage `json:"removed"`
		Changed []struct {
			Name   string   `json:"name"`
			Fields []string `json:"fields"`
		} `json:"changed"`
	}
	if err := json.Unmarshal([]byte(raw), &diff); err != nil {
		t.Fatalf("decode diff %q: %v", raw, err)
	}
	if len(diff.Added) != 0 || len(diff.Removed) != 0 {
		t.Fatalf("unexpected added/removed points: %s", raw)
	}
	if len(diff.Changed) == 0 {
		return nil
	}
	if len(diff.Changed) != 1 {
		t.Fatalf("expected one changed point, got %s", raw)
	}
	return diff.Changed[0].Fields
}
