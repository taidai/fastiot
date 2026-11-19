package industry

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWithoutStateKeepsBundledPackageActive(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !registry.Installed(EMSPackageID) {
		t.Fatal("a site without a recorded package decision keeps the bundled package active")
	}
	entry, ok := registry.Entry(EMSPackageID)
	if !ok || entry.Version != EMSPackageVersion || !entry.Installed {
		t.Fatalf("entry = %+v", entry)
	}
	if entry.InstalledAt != "" {
		t.Fatalf("bundled default must not claim an install timestamp: %q", entry.InstalledAt)
	}
}

func TestUninstallPersistsAcrossOpen(t *testing.T) {
	dir := t.TempDir()
	registry, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := registry.Uninstall(EMSPackageID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Installed {
		t.Fatal("uninstall did not report the package as absent")
	}
	if registry.Installed(EMSPackageID) {
		t.Fatal("uninstall left the package active")
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Installed(EMSPackageID) {
		t.Fatal("uninstalled state did not survive reopening the site")
	}
}

func TestInstallRejectsVersionsThisBinaryDoesNotCarry(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install("other-package", "1.0.0"); !errors.Is(err, ErrUnknownPackage) {
		t.Fatalf("unknown package install = %v", err)
	}
	if _, err := registry.Install(EMSPackageID, "9.9.9"); !errors.Is(err, ErrVersionUnavailable) {
		t.Fatalf("foreign version install = %v", err)
	}
	if !registry.Installed(EMSPackageID) {
		t.Fatal("a failed install must not change activation")
	}
}

func TestInstallAfterUninstallRecordsTheBundledVersion(t *testing.T) {
	dir := t.TempDir()
	registry, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(EMSPackageID); err != nil {
		t.Fatal(err)
	}
	entry, err := registry.Install(EMSPackageID, EMSPackageVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Installed || entry.Version != EMSPackageVersion || entry.InstalledAt == "" {
		t.Fatalf("entry = %+v", entry)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Installed(EMSPackageID) {
		t.Fatal("installed state did not survive reopening the site")
	}
}

func TestInstallAndUninstallAreIdempotent(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Install(EMSPackageID, EMSPackageVersion); err != nil {
		t.Fatalf("reinstall of the active bundled package = %v", err)
	}
	if _, err := registry.Uninstall(EMSPackageID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(EMSPackageID); err != nil {
		t.Fatalf("second uninstall = %v", err)
	}
}

func TestCorruptStateFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("a corrupt package state must not silently activate packages")
	}
}

func TestEntriesListEveryBundledPackage(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entries := registry.Entries()
	if len(entries) != len(Bundled()) || entries[0].ID != EMSPackageID || entries[0].Label == "" {
		t.Fatalf("entries = %+v", entries)
	}
}

func writeState(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A site that recorded a package version this binary does not carry must not
// start: running it would silently replace the installed semantics.
func TestOpenRejectsInstalledVersionThisBinaryDoesNotCarry(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{"version":1,"installed":{"ems":{"version":"0.9.0","installed_at":"2026-01-01T00:00:00Z"}}}`)
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "0.9.0") || !strings.Contains(err.Error(), EMSPackageVersion) {
		t.Fatalf("Open = %v, want a loud mismatch naming both versions", err)
	}
}

func TestOpenRejectsInstalledPackageThisBinaryDoesNotCarry(t *testing.T) {
	dir := t.TempDir()
	writeState(t, dir, `{"version":1,"installed":{"plant-optimizer":{"version":"1.0.0"}}}`)
	_, err := Open(dir)
	if err == nil || !strings.Contains(err.Error(), "plant-optimizer") {
		t.Fatalf("Open = %v, want the unknown package named", err)
	}
}

func TestDefaultKeepsEveryBundledPackageActive(t *testing.T) {
	registry := Default()
	for _, manifest := range Bundled() {
		if !registry.Installed(manifest.ID) {
			t.Fatalf("Default left %s inactive", manifest.ID)
		}
	}
}

func TestCheckAcceptsTheActiveBundledVersion(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Check(EMSPackageID, EMSPackageVersion); err != nil {
		t.Fatalf("Check = %v", err)
	}
}

func TestCheckRefusesPackageThisBinaryDoesNotCarry(t *testing.T) {
	registry := Default()
	err := registry.Check("plant-optimizer", "1.0.0")
	if err == nil || err.Error() != "missing_dependency:industry:plant-optimizer@1.0.0" {
		t.Fatalf("Check = %v", err)
	}
}

func TestCheckRefusesVersionThisBinaryDoesNotCarry(t *testing.T) {
	registry := Default()
	err := registry.Check(EMSPackageID, "0.9.0")
	if err == nil || !strings.Contains(err.Error(), "incompatible_dependency:industry:ems") ||
		!strings.Contains(err.Error(), "required@0.9.0") || !strings.Contains(err.Error(), "available@"+EMSPackageVersion) {
		t.Fatalf("Check = %v", err)
	}
}

func TestCheckRefusesUninstalledPackage(t *testing.T) {
	registry, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Uninstall(EMSPackageID); err != nil {
		t.Fatal(err)
	}
	if err := registry.Check(EMSPackageID, EMSPackageVersion); err == nil ||
		!strings.Contains(err.Error(), "missing_dependency:industry:ems@"+EMSPackageVersion+":not_installed") {
		t.Fatalf("Check = %v", err)
	}
}

// A binary that carries another version reports both sides instead of
// accepting the requirement or silently substituting its own version.
func TestCheckNamesRequiredAndAvailableVersions(t *testing.T) {
	registry := &Registry{
		manifests: []Manifest{{ID: EMSPackageID, Version: "1.1.0"}},
		installed: map[string]installedRecord{EMSPackageID: {Version: "1.1.0"}},
	}
	err := registry.Check(EMSPackageID, "1.0.0")
	if err == nil || err.Error() != "incompatible_dependency:industry:ems:required@1.0.0:available@1.1.0" {
		t.Fatalf("Check = %v", err)
	}
}

