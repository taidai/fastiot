package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"

	"fastiot/internal/decision"
	"fastiot/internal/ems/channel"
)

type DeliveryItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (h *Handler) deliveryCheck(w http.ResponseWriter, identity Identity) {
	nodes, err := h.visibleNodes(identity)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "check_unavailable")
		return
	}
	items := []DeliveryItem{}
	add := func(id, title string, failures, warnings []string, passText string) {
		status := "pass"
		detail := passText
		if len(failures) > 0 {
			status = "fail"
			detail = strings.Join(failures, "；")
		} else if len(warnings) > 0 {
			status = "warning"
			detail = strings.Join(warnings, "；")
		}
		items = append(items, DeliveryItem{ID: id, Title: title, Status: status, Detail: detail})
	}

	var ruleFailures, ruleWarnings, manual, disabled, busFailures, busWarnings, commandFailures []string
	now := time.Now()
	running := make(map[string]*channel.Channel)
	if h.ems != nil && h.ems.Service != nil {
		for _, ch := range h.ems.Service.Channels() {
			running[ch.ID] = ch
		}
	}
	for _, node := range nodes {
		switch node.Type {
		case "decisionTable":
			if message, _ := node.Points.Text(data.PointTypeError, ""); message != "" {
				ruleFailures = append(ruleFailures, node.Desc()+": "+message)
			}
			enabled, _ := node.Points.Value("enabled", "")
			applied, _ := node.Points.Text("applied", "")
			if enabled != 0 && applied == "" {
				ruleFailures = append(ruleFailures, node.Desc()+": 已启用但未发布")
			}
			draft, _ := node.Points.Text("draft", "")
			if draft != "" {
				var document decision.Document
				if json.Unmarshal([]byte(draft), &document) != nil || document.Validate() != nil {
					ruleWarnings = append(ruleWarnings, node.Desc()+": 草稿存在未完成绑定或非法配置")
				}
			}
			if applied != "" && draft != applied {
				ruleWarnings = append(ruleWarnings, node.Desc()+": 草稿与运行版本不同")
			}
		case data.NodeTypeVariable, data.NodeTypeModbusIO:
			if value, _ := node.Points.Value("easyemsManual", ""); value != 0 {
				manual = append(manual, node.Desc())
			}
			if value, _ := node.Points.Value(data.PointTypeDisabled, ""); value != 0 {
				disabled = append(disabled, node.Desc())
			}
		case data.NodeTypeModbus:
			children, err := client.GetNodes(h.nc, node.ID, "all", "", false)
			if err == nil && len(children) == 0 {
				busWarnings = append(busWarnings, node.Desc()+": 尚未配置点位")
			}
			if value, _ := node.Points.Value(data.PointTypeDisabled, ""); value != 0 {
				busWarnings = append(busWarnings, node.Desc()+": 连接已停用")
				continue
			}
			bus, ok := running[node.ID]
			if !ok {
				busFailures = append(busFailures, node.Desc()+": 通道未启动")
				continue
			}
			degraded, offline, unknown := 0, 0, 0
			for _, device := range bus.Devices {
				switch bus.Health(device.ID, now, h.ems.MaxAge) {
				case channel.HealthDegraded:
					degraded++
				case channel.HealthOffline:
					offline++
				case channel.HealthUnknown:
					unknown++
				}
			}
			switch {
			case len(bus.Devices) == 0:
				busWarnings = append(busWarnings, node.Desc()+": 尚未配置设备")
			case offline > 0:
				busFailures = append(busFailures, fmt.Sprintf("%s: %d 台设备离线", node.Desc(), offline))
			}
			if degraded > 0 {
				busWarnings = append(busWarnings, fmt.Sprintf("%s: %d 台设备通信不稳定", node.Desc(), degraded))
			}
			if unknown > 0 {
				busWarnings = append(busWarnings, fmt.Sprintf("%s: %d 台设备状态未知（尚未成功轮询）", node.Desc(), unknown))
			}
		}
	}
	for _, finding := range h.commandFindings(nodes) {
		commandFailures = append(commandFailures, finding.Object+": "+finding.State)
	}
	add("rules", "规则发布与运行", ruleFailures, ruleWarnings, "已启用规则均已发布，未发现运行异常")
	add("manual", "人工接管", nil, manual, "当前没有点位处于人工接管")
	add("disabled", "停用对象", nil, disabled, "当前没有点位被停用")
	add("commands", "命令结果", commandFailures, nil, "未发现失败或结果未知的命令")
	add("modbus", "Modbus 连接", busFailures, busWarnings, "Modbus 连接与轮询未发现异常")
	items = append(items,
		h.recoveryItem(),
		DeliveryItem{ID: "acceptance", Title: "现场验收", Status: "warning", Detail: "真实设备、真实品牌 A/B、独立用户与公网部署仍需现场验收"},
	)
	overall := "pass"
	for _, item := range items {
		if item.Status == "fail" {
			overall = "fail"
		} else if item.Status == "warning" && overall == "pass" {
			overall = "warning"
		}
	}
	writeJSON(w, map[string]any{"status": overall, "items": items})
}
