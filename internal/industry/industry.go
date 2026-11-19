// Package industry owns installable industry packages: the manifests this
// binary carries and the per-site record of which of them are active.
//
// The binary is the base product plus the package payloads; installing a
// package activates it for one site, and uninstalling deactivates it without
// deleting base data. A site with no recorded decision keeps the bundled
// package active so upgrading an existing deployment never removes working
// capabilities.
package industry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// EMSPackageID is the energy-management industry package.
	EMSPackageID = "ems"
	// EMSPackageVersion is the exact version this binary carries. Only this
	// version can be installed: a site never runs a package the binary cannot
	// describe.
	EMSPackageVersion = "1.0.0"
)

const stateFileName = "industry-packages.json"

var (
	// ErrUnknownPackage means no bundled package has the requested ID.
	ErrUnknownPackage = errors.New("unknown_package")
	// ErrVersionUnavailable means this binary does not carry that version.
	ErrVersionUnavailable = errors.New("package_version_mismatch")
)

// Manifest describes what one bundled package provides. Provides is operator
// facing text, not a second capability model.
type Manifest struct {
	ID       string   `json:"id"`
	Version  string   `json:"version"`
	Label    string   `json:"label"`
	Provides []string `json:"provides,omitempty"`
}

// Entry is a bundled manifest plus this site's activation state.
type Entry struct {
	Manifest
	Installed bool `json:"installed"`
	// InstalledAt is empty when the package is active only because this site
	// has never recorded a package decision.
	InstalledAt string `json:"installed_at,omitempty"`
}

// Bundled returns the packages this binary carries.
func Bundled() []Manifest {
	return []Manifest{{
		ID:      EMSPackageID,
		Version: EMSPackageVersion,
		Label:   "EMS 行业包",
		Provides: []string{
			"能源单元、设备与能力约束",
			"能源策略与电站级目标",
			"功率分配、仲裁与联合调度",
			"需量窗口与 96 点计划",
			"行业导航与验证场景",
		},
	}}
}

type installedRecord struct {
	Version     string `json:"version"`
	InstalledAt string `json:"installed_at,omitempty"`
}

type stateFile struct {
	Version   int                        `json:"version"`
	Installed map[string]installedRecord `json:"installed"`
}

// Registry is one site's package state. All methods are safe for concurrent
// use; state changes are written to disk before they are reported.
type Registry struct {
	mu        sync.Mutex
	path      string
	manifests []Manifest
	installed map[string]installedRecord
}

// Open loads the package state for a site. A missing file means the bundled
// package is active. A corrupt file is an error: an unreadable decision must
// never silently change what a site can control.
func Open(dir string) (*Registry, error) {
	registry := &Registry{
		path:      filepath.Join(dir, stateFileName),
		manifests: Bundled(),
		installed: map[string]installedRecord{},
	}
	for _, manifest := range registry.manifests {
		registry.installed[manifest.ID] = installedRecord{Version: manifest.Version}
	}
	raw, err := os.ReadFile(registry.path)
	if err != nil {
		if os.IsNotExist(err) {
			return registry, nil
		}
		return nil, fmt.Errorf("read industry package state: %w", err)
	}
	var state stateFile
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("%s: %w", stateFileName, err)
	}
	registry.installed = map[string]installedRecord{}
	for id, record := range state.Installed {
		registry.installed[id] = record
	}
	return registry, nil
}

// Installed reports whether id is active for this site.
func (r *Registry) Installed(id string) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.installed[id]
	return ok
}

// Entry returns one package's manifest and state.
func (r *Registry) Entry(id string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entry(id)
}

func (r *Registry) entry(id string) (Entry, bool) {
	manifest, ok := r.manifest(id)
	if !ok {
		return Entry{}, false
	}
	record, installed := r.installed[id]
	return Entry{Manifest: manifest, Installed: installed, InstalledAt: record.InstalledAt}, true
}

// Entries lists every bundled package with its state.
func (r *Registry) Entries() []Entry {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entries := make([]Entry, 0, len(r.manifests))
	for _, manifest := range r.manifests {
		entry, _ := r.entry(manifest.ID)
		entries = append(entries, entry)
	}
	return entries
}

// Install activates one bundled package at exactly the bundled version.
func (r *Registry) Install(id, version string) (Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, ok := r.manifest(id)
	if !ok {
		return Entry{}, ErrUnknownPackage
	}
	if version != manifest.Version {
		return Entry{}, ErrVersionUnavailable
	}
	if record, installed := r.installed[id]; installed && record.InstalledAt != "" {
		entry, _ := r.entry(id)
		return entry, nil
	}
	previous, hadPrevious := r.installed[id]
	r.installed[id] = installedRecord{Version: manifest.Version, InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	if err := r.save(); err != nil {
		if hadPrevious {
			r.installed[id] = previous
		} else {
			delete(r.installed, id)
		}
		return Entry{}, err
	}
	entry, _ := r.entry(id)
	return entry, nil
}

// Uninstall deactivates one package. It is idempotent and never deletes data;
// callers that own package data must refuse to call it while that data is in
// use.
func (r *Registry) Uninstall(id string) (Entry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.manifest(id); !ok {
		return Entry{}, ErrUnknownPackage
	}
	previous, installed := r.installed[id]
	if !installed {
		entry, _ := r.entry(id)
		return entry, nil
	}
	delete(r.installed, id)
	if err := r.save(); err != nil {
		r.installed[id] = previous
		return Entry{}, err
	}
	entry, _ := r.entry(id)
	return entry, nil
}

func (r *Registry) manifest(id string) (Manifest, bool) {
	for _, manifest := range r.manifests {
		if manifest.ID == id {
			return manifest, true
		}
	}
	return Manifest{}, false
}

func (r *Registry) save() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(stateFile{Version: 1, Installed: r.installed}, "", "  ")
	if err != nil {
		return err
	}
	temporary := r.path + ".tmp"
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, r.path)
}
