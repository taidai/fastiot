// Package alarmwatch turns existing product nodes (rules, Modbus channels,
// command results) into alarm-engine findings. It is the bridge used until the
// EMS runtime owns all channels.
package alarmwatch

import (
	"encoding/json"
	"time"

	"fastiot/internal/ems/alarm"
	"fastiot/internal/ems/health"
)

// NodeView is the small part of a node that alarm evaluation needs.
type NodeView struct {
	ID        string
	Type      string
	Text      map[string]string
	Num       map[string]float64
	Created   time.Time
	Value     float64
	HasValue  bool
	ValueTime time.Time
	Offline   bool
}

// Finding is one active alarm condition.
type Finding struct {
	Key      string
	Severity alarm.Severity
	Title    string
	Detail   string
	DeviceID string
}

// Evaluate returns the active conditions. Callers clear keys that disappear.
func Evaluate(nodes []NodeView, now time.Time) []Finding {
	findings := make([]Finding, 0)
	for _, node := range nodes {
		switch node.Type {
		case "decisionTable":
			if detail := node.Text["error"]; detail != "" {
				findings = append(findings, Finding{
					Key: "rule-error:" + node.ID, Severity: alarm.Critical,
					Title: "规则运行异常", Detail: detail, DeviceID: node.ID,
				})
			}
		case "modbus":
			if node.Num["disabled"] != 0 {
				continue
			}
			since, found := node.Num["lastGoodPoll"]
			lastSuccess := time.Time{}
			if found && since > 0 {
				lastSuccess = time.Unix(int64(since), 0)
			}
			verdict := health.Evaluate(health.Input{
				Requests: 1, ConsecutiveFailures: 1, LastSuccess: lastSuccess,
				PollPeriod: time.Duration(node.Num["pollPeriod"]) * time.Millisecond,
				Timeout:    time.Duration(node.Num["timeout"]) * time.Millisecond,
				CreatedAt:  node.Created,
			}, now)
			if verdict.State == health.Offline {
				findings = append(findings, Finding{
					Key: "bus-offline:" + node.ID, Severity: alarm.Critical,
					Title: "连接中断", Detail: verdict.Reason, DeviceID: node.ID,
				})
			}
		case "variable", "modbusIo":
			text := node.Text["lastCommand"]
			if text == "" {
				continue
			}
			var command struct {
				Status string `json:"status"`
			}
			if json.Unmarshal([]byte(text), &command) != nil {
				continue
			}
			switch command.Status {
			case "failed", "unknown":
				findings = append(findings, Finding{
					Key: "command-" + command.Status + ":" + node.ID, Severity: alarm.Warning,
					Title: "控制结果异常", Detail: "命令状态：" + command.Status, DeviceID: node.ID,
				})
			}
		}
	}
	return findings
}
