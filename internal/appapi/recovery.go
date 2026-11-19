package appapi

import (
	"fmt"
	"net/http"
	"time"

	"fastiot/internal/backup"
)

// 备份与恢复是离线操作：备份和恢复都必须先停止实例，因此本接口只报告本机
// 真实记录（最近一次备份、最近一次恢复），不提供在线热备份，也不虚构时间。

func (h *Handler) recoveryStatus(w http.ResponseWriter, _ Identity) {
	if h.dataDir == "" {
		writeError(w, http.StatusInternalServerError, "data_dir_unavailable")
		return
	}
	writeJSON(w, backup.ReadStatus(h.dataDir))
}

func displayTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.Local().Format("2006-01-02 15:04:05")
}

func (h *Handler) recoveryItem() DeliveryItem {
	if h.dataDir == "" {
		return DeliveryItem{
			ID: "backup", Title: "备份与恢复", Status: "warning",
			Detail: "尚未记录离线完整备份；停机执行 fastiot backup 并定期做恢复演练，应用不虚构最近备份时间",
		}
	}
	status := backup.ReadStatus(h.dataDir)
	switch {
	case status.Restored && status.Recovery != nil:
		return DeliveryItem{
			ID: "backup", Title: "备份与恢复", Status: "warning",
			Detail: fmt.Sprintf("本实例于 %s 从 %s 的快照恢复；快照之后的历史、报警、审计与工程变更不在其中",
				displayTime(status.Recovery.RestoredAt), displayTime(status.Recovery.SnapshotAt)),
		}
	case status.Backup != nil:
		return DeliveryItem{
			ID: "backup", Title: "备份与恢复", Status: "pass",
			Detail: fmt.Sprintf("最近一次离线备份：%s（%d 个文件）；恢复会替换整个数据目录",
				displayTime(status.Backup.CreatedAt), status.Backup.Files),
		}
	default:
		return DeliveryItem{
			ID: "backup", Title: "备份与恢复", Status: "warning",
			Detail: "尚未记录离线完整备份；停机执行 fastiot backup 并定期做恢复演练，应用不虚构最近备份时间",
		}
	}
}

func (h *Handler) backupGate() preflightGate {
	if h.dataDir == "" {
		return preflightGate{
			Key: "backup", Title: "备份", Status: "warn", Informational: true,
			Detail:   "尚未记录离线完整备份",
			NextStep: "停机执行 fastiot backup，并定期用 fastiot restore 做恢复演练",
		}
	}
	status := backup.ReadStatus(h.dataDir)
	switch {
	case status.Restored && status.Recovery != nil:
		return preflightGate{
			Key: "backup", Title: "备份", Status: "warn", Informational: true,
			Detail:   fmt.Sprintf("实例恢复自 %s 的快照；快照之后的数据不在其中", displayTime(status.Recovery.SnapshotAt)),
			NextStep: "确认数据损失窗口，再生成并保存新的离线备份",
		}
	case status.Backup != nil:
		return preflightGate{
			Key: "backup", Title: "备份", Status: "pass", Informational: true,
			Detail:   fmt.Sprintf("最近一次离线备份：%s（%d 个文件）", displayTime(status.Backup.CreatedAt), status.Backup.Files),
			NextStep: "快照之后的运行数据需重新备份",
		}
	default:
		return preflightGate{
			Key: "backup", Title: "备份", Status: "warn", Informational: true,
			Detail:   "尚未记录离线完整备份",
			NextStep: "停机执行 fastiot backup，并定期用 fastiot restore 做恢复演练",
		}
	}
}
