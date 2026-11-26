package appapi

import (
	"net"
	"sort"
	"strings"
	"time"

	"github.com/simpleiot/simpleiot/data"
)

// 验收证据清单：把这次导出的范围、版本、指纹、部署记录与采集证据分类
// 写成机器可核对的 JSON，便于复核"这份文件当时对应哪个版本、哪些对象"。
const acceptanceManifestFormat = "fastiot.acceptance-evidence"

type acceptanceScope struct {
	Admin   bool     `json:"admin"`
	Anchors []string `json:"anchors"`
	Actions []string `json:"actions"`
}

type acceptanceChannel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Address       string `json:"address"`
	EvidenceClass string `json:"evidence_class"`
}

type acceptanceVersion struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Deliverable bool   `json:"deliverable"`
	CreatedAt   string `json:"created_at,omitempty"`
}

type acceptancePreflight struct {
	Status      string `json:"status"`
	Fingerprint string `json:"fingerprint"`
	ID          string `json:"id,omitempty"`
}

type acceptanceSite struct {
	SiteID      string                `json:"site_id"`
	SiteName    string                `json:"site_name"`
	Version     *acceptanceVersion    `json:"version"`
	Preflight   acceptancePreflight   `json:"preflight"`
	Channels    []acceptanceChannel   `json:"channels"`
	Deployments []deploymentRecord    `json:"deployments"`
	Simulation  int                   `json:"simulation_channels"`
	Field       int                   `json:"field_channels"`
	Unbound     int                   `json:"unbound_channels"`
}

type acceptanceStatistics struct {
	AlarmWindowDays int    `json:"alarm_window_days"`
	QualitySource   string `json:"quality_source"`
	GeneratedAt     string `json:"generated_at"`
}

type acceptanceManifest struct {
	Format      string               `json:"format"`
	Version     int                  `json:"version"`
	ExportedAt  string               `json:"exported_at"`
	Actor       string               `json:"actor"`
	Scope       acceptanceScope      `json:"scope"`
	Sites       []acceptanceSite     `json:"sites"`
	Statistics  acceptanceStatistics `json:"statistics"`
	Disclosure  string               `json:"disclosure"`
}

// 回环地址上的采集只可能是仿真/本机验证，不能当成现场证据。
func channelEvidenceClass(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return "unbound"
	}
	host := address
	if parsed, _, err := net.SplitHostPort(address); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	switch host {
	case "127.0.0.1", "::1", "localhost":
		return "simulation"
	}
	return "field"
}

func (h *Handler) acceptanceManifest(identity Identity, nodes []data.NodeEdge, now time.Time, actor string) (acceptanceManifest, error) {
	manifest := acceptanceManifest{
		Format: acceptanceManifestFormat, Version: 1,
		ExportedAt: now.UTC().Format(time.RFC3339Nano), Actor: actor,
		Scope: acceptanceScope{Admin: identity.Policy.Admin, Anchors: append([]string{}, identity.Anchors...), Actions: append([]string{}, identity.Actions...)},
		Sites: []acceptanceSite{},
		Statistics: acceptanceStatistics{
			AlarmWindowDays: 30,
			QualitySource:   "本次运行期间的轮询计数；进程重启后清零",
			GeneratedAt:     now.UTC().Format(time.RFC3339Nano),
		},
		Disclosure: "回环（127.0.0.1/::1/localhost）通道标记为 simulation，只代表本机模拟器验证；非回环通道标记为 field，但本包不构成真实设备现场签收。",
	}
	sites := []data.NodeEdge{}
	for _, node := range nodes {
		if node.Type == NodeTypeSite && h.allows(identity, node.ID) {
			sites = append(sites, node)
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	for _, site := range sites {
		entry := acceptanceSite{SiteID: site.ID, SiteName: nodeName(site), Channels: []acceptanceChannel{}, Deployments: []deploymentRecord{}}
		for _, node := range nodes {
			if node.Type != data.NodeTypeModbus || !h.users.IsUnder(node.ID, site.ID) {
				continue
			}
			address := textPoint(node.Points, data.PointTypeURI)
			class := channelEvidenceClass(address)
			switch class {
			case "simulation":
				entry.Simulation++
			case "field":
				entry.Field++
			default:
				entry.Unbound++
			}
			entry.Channels = append(entry.Channels, acceptanceChannel{ID: node.ID, Name: nodeName(node), Address: address, EvidenceClass: class})
		}
		if summaries, err := h.listVersions(site.ID); err == nil && len(summaries) > 0 {
			latest := summaries[0]
			for _, summary := range summaries[1:] {
				if summary.CreatedAt.After(latest.CreatedAt) {
					latest = summary
				}
			}
			fingerprint := latest.Fingerprint
			if fingerprint == "" {
				if version, err := h.loadVersion(site.ID, latest.ID); err == nil {
					fingerprint = deploymentStructureFingerprint(version)
				}
			}
			entry.Version = &acceptanceVersion{ID: latest.ID, Fingerprint: fingerprint, Deliverable: latest.Deliverable, CreatedAt: latest.CreatedAt.UTC().Format(time.RFC3339Nano)}
		}
		if report, err := h.buildPreflight(identity, site.ID); err == nil {
			entry.Preflight = acceptancePreflight{Status: report.Status, Fingerprint: report.Fingerprint}
		}
		if records, err := h.readDeploymentRecords(site.ID); err == nil {
			for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
				records[i], records[j] = records[j], records[i]
			}
			entry.Deployments = records
		}
		manifest.Sites = append(manifest.Sites, entry)
	}
	return manifest, nil
}
