package industry

import (
	"errors"
	"os"
	"path/filepath"
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
