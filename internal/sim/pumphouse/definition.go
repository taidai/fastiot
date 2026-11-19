package pumphouse

import (
	"encoding/json"

	"fastiot/internal/ems/alarmtype"
	"fastiot/internal/ems/channel"
	"fastiot/internal/ems/profile"
	"fastiot/internal/ems/safety"
)

// The sample's business vocabulary. The point names are the keys the unit
// model maps; they are also what an operator sees, so they stay in Chinese.
const (
	PointLevel        = "水箱液位"
	PointPressure     = "出口压力"
	PointPump1Current = "1#泵电流"
	PointPump2Current = "2#泵电流"
	PointPump1Command = "1#泵启停命令"
	PointPump2Command = "2#泵启停命令"
	PointPump1Starts  = "1#泵启动次数"
	PointPump2Starts  = "2#泵启动次数"
	PointPump1Run     = "1#泵运行反馈"
	PointPump1Remote  = "1#泵远程模式"
	PointPump1Fault   = "1#泵故障"
	PointPump2Run     = "2#泵运行反馈"
	PointPump2Remote  = "2#泵远程模式"
	PointPump2Fault   = "2#泵故障"
)

// ProfileID is the custom profile the sample binds to station 1.
const ProfileID = "pumphouse-sim"

// SampleName labels the group and screen so the simulation is never mistaken
// for a real station.
const SampleName = "泵房样板（模拟设备）"

// SamplePoints is the station's point table, in profile/CSV vocabulary.
var SamplePoints = []profile.PointSpec{
	{Name: PointLevel, Function: channel.HoldingRegister, Address: AddressLevel, Format: "uint16", Scale: 0.1, Unit: "%", ReadOnly: true, Role: profile.RoleTelemetry},
	{Name: PointPressure, Function: channel.HoldingRegister, Address: AddressPressure, Format: "uint16", Scale: 0.01, Unit: "MPa", ReadOnly: true, Role: profile.RoleTelemetry},
	{Name: PointPump1Current, Function: channel.HoldingRegister, Address: AddressPump1Current, Format: "uint16", Scale: 0.1, Unit: "A", ReadOnly: true, Role: profile.RoleTelemetry},
	{Name: PointPump2Current, Function: channel.HoldingRegister, Address: AddressPump2Current, Format: "uint16", Scale: 0.1, Unit: "A", ReadOnly: true, Role: profile.RoleTelemetry},
	{Name: PointPump1Command, Function: channel.HoldingRegister, Address: AddressPump1Command, Format: "uint16", Role: profile.RoleSetpoint, Note: "写 1 启动、0 停止；模拟控制器接受后由运行反馈证明结果"},
	{Name: PointPump2Command, Function: channel.HoldingRegister, Address: AddressPump2Command, Format: "uint16", Role: profile.RoleSetpoint, Note: "写 1 启动、0 停止"},
	{Name: PointPump1Starts, Function: channel.HoldingRegister, Address: AddressPump1Starts, Format: "uint16", ReadOnly: true, Role: profile.RoleTelemetry, Note: "启动次数"},
	{Name: PointPump2Starts, Function: channel.HoldingRegister, Address: AddressPump2Starts, Format: "uint16", ReadOnly: true, Role: profile.RoleTelemetry, Note: "启动次数"},
	{Name: PointPump1Run, Function: channel.DiscreteInput, Address: BitPump1Run, ReadOnly: true, Role: profile.RoleStatus},
	{Name: PointPump1Remote, Function: channel.DiscreteInput, Address: BitPump1Remote, ReadOnly: true, Role: profile.RoleStatus, Note: "1 = 远程，0 = 就地"},
	{Name: PointPump1Fault, Function: channel.DiscreteInput, Address: BitPump1Fault, ReadOnly: true, Role: profile.RoleStatus},
	{Name: PointPump2Run, Function: channel.DiscreteInput, Address: BitPump2Run, ReadOnly: true, Role: profile.RoleStatus},
	{Name: PointPump2Remote, Function: channel.DiscreteInput, Address: BitPump2Remote, ReadOnly: true, Role: profile.RoleStatus, Note: "1 = 远程，0 = 就地"},
	{Name: PointPump2Fault, Function: channel.DiscreteInput, Address: BitPump2Fault, ReadOnly: true, Role: profile.RoleStatus},
}

// SampleCommands start and stop each pump. Confirmation comes from the run
// feedback, not from the command word, so a pump that does not actually start
// never confirms the command.
var SampleCommands = []profile.CommandSpec{
	{ID: "start-1", Name: "启动 1#泵", Point: PointPump1Command, Value: 1, Readback: PointPump1Run, ReadbackValue: 1, TimeoutMS: 6000},
	{ID: "stop-1", Name: "停止 1#泵", Point: PointPump1Command, Value: 0, Readback: PointPump1Run, ReadbackValue: 0, TimeoutMS: 6000},
	{ID: "start-2", Name: "启动 2#泵", Point: PointPump2Command, Value: 1, Readback: PointPump2Run, ReadbackValue: 1, TimeoutMS: 6000},
	{ID: "stop-2", Name: "停止 2#泵", Point: PointPump2Command, Value: 0, Readback: PointPump2Run, ReadbackValue: 0, TimeoutMS: 6000},
}

// ScreenTiles names the points shown on the sample's running screen.
var ScreenTiles = []string{
	PointLevel, PointPressure,
	PointPump1Run, PointPump2Run,
	PointPump1Remote, PointPump2Remote,
	PointPump1Fault, PointPump2Fault,
	PointPump1Current, PointPump2Current,
}

// SampleProfile returns the profile a delivery engineer binds to station 1.
func SampleProfile() profile.Profile {
	return profile.Profile{
		ID: ProfileID, Vendor: "FastIOT", Model: "两泵一水箱模拟控制器", Protocol: "modbus",
		Points: len(SamplePoints), Commands: len(SampleCommands),
		Verification: "软件模拟设备；不是厂商协议文档，不能作为真机证据",
		Notes: []string{
			"本 Profile 只用于软件样板，不得连接到真实泵站。",
			"启停命令写入保持寄存器，运行反馈来自离散输入；就地/远程与故障也是离散输入。",
		},
		PointSpecs: SamplePoints, CommandSpecs: SampleCommands,
	}
}

// SampleProfileJSON is the file written to <data>/profiles/ before startup.
func SampleProfileJSON() ([]byte, error) {
	return json.MarshalIndent(SampleProfile(), "", "  ")
}

// PointTableCSV is the point table imported through the existing CSV API.
func PointTableCSV(serverID int) []byte {
	return SampleProfile().CSV(serverID)
}

// AlarmTemplate is one alarm the sample creates. PointName is resolved to a
// node id when the project is built.
type AlarmTemplate struct {
	Code         string
	Name         string
	Group        string
	Severity     string
	Trigger      string
	PointName    string
	Value        float64
	DelaySeconds int
	Prompt       string
}

// AlarmTemplates cover both pump faults and a low tank level.
var AlarmTemplates = []AlarmTemplate{
	{Code: "PUMP1-FAULT", Name: "1#泵故障", Group: "泵房", Severity: "critical", Trigger: "eq", PointName: PointPump1Fault, Value: 1, Prompt: "检查 1#泵保护动作并确认就地状态"},
	{Code: "PUMP2-FAULT", Name: "2#泵故障", Group: "泵房", Severity: "critical", Trigger: "eq", PointName: PointPump2Fault, Value: 1, Prompt: "检查 2#泵保护动作并确认就地状态"},
	{Code: "TANK-LOW", Name: "水箱液位低", Group: "泵房", Severity: "warning", Trigger: "lt", PointName: PointLevel, Value: 20, DelaySeconds: 5, Prompt: "检查补水或启动备用泵"},
}

// AlarmConfigs resolves the templates to the alarm-type API payloads.
func AlarmConfigs(pointIDs map[string]string) []alarmtype.Config {
	configs := make([]alarmtype.Config, 0, len(AlarmTemplates))
	for _, template := range AlarmTemplates {
		configs = append(configs, alarmtype.Config{
			Code: template.Code, Name: template.Name, Group: template.Group,
			Severity: template.Severity, Prompt: template.Prompt, Trigger: template.Trigger,
			PointID: pointIDs[template.PointName], Value: template.Value,
			DelaySeconds: template.DelaySeconds, Confirm: "auto", RecordRecovery: true, Enabled: true,
		})
	}
	return configs
}

// StartSafety keeps a pump from being started on an empty tank. It is attached
// to each start/stop command point so the shared safety gate judges it.
func StartSafety(levelPointID string) safety.Config {
	return safety.Config{Conditions: []safety.ConditionConfig{
		{PointID: levelPointID, Operator: "gt", Value: 5, Kind: "number"},
	}}
}
