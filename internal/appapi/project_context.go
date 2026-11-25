package appapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"fastiot/internal/industry"
	"github.com/simpleiot/simpleiot/client"
	"github.com/simpleiot/simpleiot/data"
)

// A local runtime has one durable project root, not one project per Site or
// per authorization anchor. The runtime association is deliberately local;
// it is not a deployment record or a remote-instance registry.
type projectContext struct {
	ID           string               `json:"id"`
	RootID       string               `json:"root_id"`
	Name         string               `json:"name"`
	ScopeRootIDs []string             `json:"scope_root_ids"`
	Runtime      localProjectRuntime  `json:"runtime"`
	VersionScope string               `json:"version_scope"`
	Sites        []projectContextSite `json:"sites"`
	Dependencies []packageDependency  `json:"dependencies"`
}
type localProjectRuntime struct {
	Kind   string `json:"kind"`
	RootID string `json:"root_id"`
}
type projectContextSite struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	LatestVersion *string `json:"latest_version"`
}

func (h *Handler) projectDirectory(w http.ResponseWriter, identity Identity) {
	root, err := client.GetRootNode(h.nc)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project_context_unavailable")
		return
	}
	scoped := h.identityInProject(identity, root.ID)
	if len(scoped.Anchors) == 0 {
		writeError(w, http.StatusForbidden, "no_project_access")
		return
	}
	nodes, err := h.visibleNodes(scoped)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "project_context_unavailable")
		return
	}
	project := projectContext{
		ID: root.ID, RootID: root.ID, Name: nodeName(root), ScopeRootIDs: scoped.Anchors,
		Runtime:      localProjectRuntime{Kind: "local", RootID: root.ID},
		VersionScope: "site", Sites: []projectContextSite{}, Dependencies: []packageDependency{},
	}
	profiles := map[string]bool{}
	for _, node := range nodes {
		if node.Type == NodeTypeSite {
			site := projectContextSite{ID: node.ID, Name: nodeName(node)}
			versions, err := h.listVersions(node.ID)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "project_versions_unavailable")
				return
			}
			if len(versions) > 0 {
				site.LatestVersion = &versions[0].ID
			}
			project.Sites = append(project.Sites, site)
		}
		if node.Type == data.NodeTypeModbus {
			for _, point := range node.Points {
				if point.Type != unitModelPoint {
					continue
				}
				var model unitModelConfig
				if json.Unmarshal([]byte(point.Txt()), &model) == nil && model.ProfileID != "" {
					profiles[model.ProfileID] = true
				}
			}
		}
	}
	for id := range profiles {
		version, _ := h.profileVersion(id)
		project.Dependencies = append(project.Dependencies, packageDependency{Kind: dependencyKindProfile, ID: id, Version: version})
	}
	if packageNeedsIndustry(nodes) {
		project.Dependencies = append(project.Dependencies, packageDependency{Kind: dependencyKindIndustry, ID: industry.EMSPackageID, Version: industry.EMSPackageVersion})
	}
	sort.Slice(project.Sites, func(i, j int) bool { return project.Sites[i].ID < project.Sites[j].ID })
	sort.Slice(project.Dependencies, func(i, j int) bool {
		return project.Dependencies[i].Kind+project.Dependencies[i].ID < project.Dependencies[j].Kind+project.Dependencies[j].ID
	})
	writeJSON(w, map[string]any{"user_id": identity.UserID, "projects": []projectContext{project}})
}

func (h *Handler) identityInProject(identity Identity, rootID string) Identity {
	anchors := []string{}
	for _, anchor := range identity.Anchors {
		if anchor == rootID || h.users.IsUnder(anchor, rootID) {
			anchors = append(anchors, anchor)
		}
	}
	identity.Anchors = anchors
	return identity
}
