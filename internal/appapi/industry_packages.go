package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"fastiot/internal/industry"
)

// ReasonPackageNotInstalled is the stable denial for a route owned by an
// industry package that this site has not installed. It is a capability
// boundary, not a permission error: retrying with another account cannot help.
const ReasonPackageNotInstalled = "industry_package_not_installed"

// packageInstalled reports whether an industry package owns the route and is
// active. An empty package means the base product, which is always available.
func (h *Handler) packageInstalled(id string) bool {
	if id == "" {
		return true
	}
	registry := h.packages.Load()
	if registry == nil {
		return true
	}
	return registry.Installed(id)
}

func (h *Handler) industryPackages(w http.ResponseWriter) {
	registry := h.packages.Load()
	if registry == nil {
		writeError(w, http.StatusServiceUnavailable, "packages_unavailable")
		return
	}
	writeJSON(w, map[string]any{"packages": registry.Entries()})
}

func (h *Handler) installIndustryPackage(w http.ResponseWriter, r *http.Request) {
	registry := h.packages.Load()
	if registry == nil {
		writeError(w, http.StatusServiceUnavailable, "packages_unavailable")
		return
	}
	var input struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	entry, err := registry.Install(input.ID, input.Version)
	switch {
	case err == nil:
		writeJSON(w, entry)
	case errors.Is(err, industry.ErrVersionUnavailable):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, industry.ErrUnknownPackage):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "package_install_failed")
	}
}

func (h *Handler) uninstallIndustryPackage(w http.ResponseWriter, r *http.Request, identity Identity, id string) {
	registry := h.packages.Load()
	if registry == nil {
		writeError(w, http.StatusServiceUnavailable, "packages_unavailable")
		return
	}
	if id == industry.EMSPackageID {
		if blockers := h.packageDataInUse(identity); len(blockers) > 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "package_in_use", "blockers": blockers})
			return
		}
	}
	entry, err := registry.Uninstall(id)
	switch {
	case err == nil:
		writeJSON(w, entry)
	case errors.Is(err, industry.ErrUnknownPackage):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "package_uninstall_failed")
	}
}

// packageDataInUse lists live industry configuration that an uninstall would
// orphan. Empty means uninstall is safe. A read failure refuses the uninstall:
// the operator must be able to prove no control configuration is active.
func (h *Handler) packageDataInUse(identity Identity) []string {
	nodes, err := h.visibleNodes(identity)
	if err != nil {
		return []string{"无法读取站点数据，拒绝卸载（不能确认已无行业配置在运行）"}
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	count := func(nodeID, label, key string) {
		token := nodeID + "/" + key
		if seen[token] {
			return
		}
		seen[token] = true
		counts[label]++
	}
	for _, node := range nodes {
		switch node.Type {
		case NodeTypeEnergyUnit:
			count(node.ID, "能源单元", node.Type)
		case NodeTypePlant:
			count(node.ID, "电站", node.Type)
		case NodeTypeResourceGroup:
			count(node.ID, "资源集群", node.Type)
		}
		for _, point := range node.Points {
			if point.Tombstone%2 != 0 {
				continue
			}
			switch point.Type {
			case "easyemsAllocator":
				count(node.ID, "功率分配器", point.Type)
			case plan96Point:
				count(node.ID, "96 点计划", point.Type)
			case "easyemsTariff":
				count(node.ID, "站点电价", point.Type)
			case PointTypeSiteTarget, PointTypeSiteConstraint, PointTypePlantTarget, PointTypePlantConstraint, PointTypePCCFeedback:
				count(node.ID, "电站/站点目标与约束", point.Type)
			}
		}
	}
	labels := make([]string, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	blockers := make([]string, 0, len(labels))
	for _, label := range labels {
		blockers = append(blockers, label+" "+strconv.Itoa(counts[label])+" 项")
	}
	return blockers
}
