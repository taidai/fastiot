package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"fastiot/internal/authz"
	domainaccess "fastiot/internal/domain/access"
	"fastiot/internal/industry"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// ReasonPackageNotInstalled is the stable denial for a route owned by an
// industry package that this site has not installed. It is a capability
// boundary, not a permission error: retrying with another account cannot help.
const ReasonPackageNotInstalled = "industry_package_not_installed"
const ReasonProjectIndustryUnavailable = "project_industry_dependency_unavailable"

type projectIndustryState struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
	Required  bool   `json:"required"`
	Blocked   bool   `json:"blocked"`
}

func (h *Handler) projectIndustry(required bool) projectIndustryState {
	installed := h.packageInstalled(industry.EMSPackageID)
	return projectIndustryState{industry.EMSPackageID, industry.EMSPackageVersion, installed, required, required && !installed}
}

// Ancestors convey inherited dependency only, never extra object access. Do not
// inspect sibling Sites or add ancestors to the caller's anchors/snapshot.
func (h *Handler) projectRequiresIndustry(identity Identity, nodes []data.NodeEdge) (bool, error) {
	if packageNeedsIndustry(nodes) {
		return true, nil
	}
	root, err := client.GetRootNode(h.nc)
	if err != nil {
		return false, err
	}
	for _, anchor := range identity.Anchors {
		seen := map[string]bool{}
		for id := anchor; ; {
			if id == "" || id == "root" || seen[id] {
				return false, errors.New("invalid_industry_ancestry")
			}
			seen[id] = true
			parents, err := client.GetNodes(h.nc, "all", id, "", false)
			if err != nil {
				return false, err
			}
			if len(parents) != 1 {
				return false, errors.New("ambiguous_industry_ancestry")
			}
			if packageNeedsIndustry(parents) {
				return true, nil
			}
			if id == root.ID {
				break
			}
			id = parents[0].Parent
		}
	}
	return false, nil
}

// Reads, diagnostics, alarm handling and system recovery are not control or
// engineering activation. Keep them available on a blocked project.
func industryDependentMutation(r *http.Request, route authz.Requirement) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return false
	}
	switch route.Action {
	case authz.Configure, authz.Command, authz.WriteValue, authz.RulePublish, authz.Delete,
		authz.VirtualInputApply, authz.VirtualInputWrite, authz.CalculationApply:
		return true
	}
	return false
}

func (h *Handler) requireProjectIndustry(w http.ResponseWriter, identity Identity) bool {
	if h.packageInstalled(industry.EMSPackageID) {
		return true
	}
	nodes, err := h.visibleNodes(identity)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project_context_unavailable")
		return false
	}
	required, err := h.projectRequiresIndustry(identity, nodes)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project_context_unavailable")
		return false
	}
	if required {
		writeError(w, http.StatusConflict, ReasonProjectIndustryUnavailable)
		return false
	}
	return true
}

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

// packagesOrBundled returns the site's package state, falling back to every
// bundled package active when no registry is wired (direct handler embeddings).
// The fallback keeps dependency checks on the same registry as route gating.
func (h *Handler) packagesOrBundled() *industry.Registry {
	if registry := h.packages.Load(); registry != nil {
		return registry
	}
	return industry.Default()
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

func templateNeedsIndustry(nodes []domainaccess.TemplateNode) bool {
	for _, node := range nodes {
		if packageNeedsIndustry([]data.NodeEdge{{Type: node.Type}}) || templateNeedsIndustry(node.Children) {
			return true
		}
	}
	return false
}

// industryConfiguration is the single content classifier for dependency pins,
// activation blockers and workbench gates. Activation never changes ownership.
func industryConfiguration(node data.NodeEdge) []string {
	var labels []string
	switch node.Type {
	case NodeTypeEnergyUnit:
		labels = append(labels, "能源单元")
	case NodeTypePlant:
		labels = append(labels, "电站")
	case NodeTypeResourceGroup:
		labels = append(labels, "资源集群")
	}
	for _, point := range node.Points {
		if point.Tombstone%2 != 0 {
			continue
		}
		switch point.Type {
		case "easyemsAllocator":
			labels = append(labels, "功率分配器")
		case plan96Point:
			labels = append(labels, "96 点计划")
		case "easyemsTariff":
			labels = append(labels, "站点电价")
		case PointTypeSiteTarget, PointTypeSiteConstraint, PointTypePlantTarget, PointTypePlantConstraint, PointTypePCCFeedback:
			labels = append(labels, "电站/站点目标与约束")
		}
	}
	return labels
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
		for _, label := range industryConfiguration(node) {
			count(node.ID, label, label)
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
