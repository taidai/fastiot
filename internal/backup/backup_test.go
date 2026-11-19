package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fastiot/internal/datadir"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// seedDataDir lays out the files a real instance keeps: audit log, alarm
// events, project versions, industry package state and the JetStream store.
func seedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "audit.jsonl"),
		`{"time":"2026-11-18T10:00:00Z","actor":"admin","action":"登录","result":"success"}`+"\n"+
			`{"time":"2026-11-19T11:30:00Z","actor":"admin","action":"项目导出","result":"success"}`+"\n")
	writeFile(t, filepath.Join(dir, "alarm-events.jsonl"),
		`{"time":"2026-11-19T09:15:00Z","title":"A high","transition":"raised","state":"active"}`+"\n")
	writeFile(t, filepath.Join(dir, "projects", "site-a", "pv-1.json"), `{"id":"pv-1","site_id":"site-a"}`)
	writeFile(t, filepath.Join(dir, "projects", "site-a", "pv-2.json"), `{"id":"pv-2","site_id":"site-a"}`)
	writeFile(t, filepath.Join(dir, "industry-packages.json"), `{"version":1,"installed":{"ems":{"version":"1.0.0"}}}`)
	writeFile(t, filepath.Join(dir, "initial-password.txt"), "账号: admin\n密码: secret\n")
	writeFile(t, filepath.Join(dir, "jetstream", "000001.jetstream"), "store-bytes")
	// A stale claim from a process that is not running must not enter the
	// archive: it is runtime state, and carrying it would look like a live
	// instance after restore.
	writeFile(t, filepath.Join(dir, datadir.Marker), `{"pid":99999999,"started_at":"2026-11-19T00:00:00Z"}`)
	return dir
}

func createSeed(t *testing.T) (string, string, Manifest) {
	t.Helper()
	dir := seedDataDir(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	manifest, err := Create(dir, archive, "test-version")
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	return dir, archive, manifest
}

func archiveEntries(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	entries := []string{}
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			break
		}
		entries = append(entries, header.Name)
	}
	return entries
}

func writeTestArchive(t *testing.T, path string, entries []struct{ name, body string }) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
}

func validManifest(t *testing.T, created time.Time, files int) string {
	t.Helper()
	manifest := Manifest{
		Format: Format, FormatVersion: FormatVersion, CreatedAt: created,
		DataDir: "data", Files: files,
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestCreateRefusesWhileAnInstanceHoldsTheDataDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := datadir.Write(dir, "running"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { datadir.Remove(dir) })
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	_, err := Create(dir, archive, "test-version")
	if !errors.Is(err, ErrInstanceRunning) {
		t.Fatalf("create while running = %v, want ErrInstanceRunning", err)
	}
	if _, statErr := os.Stat(archive); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("a refused backup must not leave an archive")
	}
}

func TestCreateRefusesMissingDataDirectory(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	if _, err := Create(filepath.Join(t.TempDir(), "missing"), archive, "test-version"); !errors.Is(err, ErrDataDirMissing) {
		t.Fatalf("create on missing dir = %v, want ErrDataDirMissing", err)
	}
}

func TestBackupManifestCapturesWhatTheSnapshotContains(t *testing.T) {
	dir, archive, manifest := createSeed(t)

	if manifest.Format != Format || manifest.FormatVersion != FormatVersion {
		t.Fatalf("format = %s/%d", manifest.Format, manifest.FormatVersion)
	}
	if manifest.CreatedAt.IsZero() {
		t.Fatal("snapshot time is missing")
	}
	if manifest.DataDir != filepath.Base(dir) {
		t.Fatalf("data dir = %q", manifest.DataDir)
	}
	if manifest.Files < 5 || manifest.Bytes == 0 {
		t.Fatalf("files = %d, bytes = %d", manifest.Files, manifest.Bytes)
	}
	inventory := manifest.Inventory
	if inventory.AuditRecords != 2 || inventory.AuditFirst != "2026-11-18T10:00:00Z" || inventory.AuditLast != "2026-11-19T11:30:00Z" {
		t.Fatalf("audit inventory = %#v", inventory)
	}
	if inventory.AlarmEvents != 1 || inventory.AlarmFirst != "2026-11-19T09:15:00Z" {
		t.Fatalf("alarm inventory = %#v", inventory)
	}
	if inventory.ProjectSites != 1 || inventory.ProjectVersions != 2 {
		t.Fatalf("project inventory = %#v", inventory)
	}
	if len(inventory.IndustryPackages) != 1 || inventory.IndustryPackages[0] != "ems@1.0.0" {
		t.Fatalf("industry inventory = %#v", inventory)
	}
	for _, entry := range archiveEntries(t, archive) {
		if entry == datadir.Marker {
			t.Fatal("runtime ownership marker entered the backup")
		}
	}

	// The local record is what the running instance reports; it must match.
	raw, err := os.ReadFile(filepath.Join(dir, RecordName))
	if err != nil {
		t.Fatalf("backup record: %v", err)
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	if !record.CreatedAt.Equal(manifest.CreatedAt) || record.Files != manifest.Files {
		t.Fatalf("record = %#v, manifest = %#v", record, manifest)
	}

	read, err := ReadManifest(archive)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !read.CreatedAt.Equal(manifest.CreatedAt) || read.Files != manifest.Files {
		t.Fatalf("read manifest = %#v", read)
	}
}

func TestRestoreToCleanDirectoryRecordsSnapshotAndDataLoss(t *testing.T) {
	_, archive, manifest := createSeed(t)
	target := filepath.Join(t.TempDir(), "data")

	recovery, err := Restore(archive, target)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !recovery.SnapshotAt.Equal(manifest.CreatedAt) {
		t.Fatalf("snapshot = %v, manifest = %v", recovery.SnapshotAt, manifest.CreatedAt)
	}
	if recovery.RestoredAt.Before(recovery.SnapshotAt) {
		t.Fatal("restore time is before the snapshot it restored")
	}
	if !recovery.DataLoss.After.Equal(recovery.SnapshotAt) || !recovery.DataLoss.Before.Equal(recovery.RestoredAt) {
		t.Fatalf("data loss window = %#v", recovery.DataLoss)
	}
	if recovery.DataLoss.Detail == "" || !strings.Contains(recovery.DataLoss.Detail, "不在本次恢复") {
		t.Fatalf("data loss detail = %q", recovery.DataLoss.Detail)
	}
	if !strings.Contains(recovery.NotVersionRollback, "版本回退") || len(recovery.Includes) == 0 {
		t.Fatalf("recovery text = %#v", recovery)
	}
	if recovery.SnapshotInventory.AuditRecords != 2 || recovery.SnapshotInventory.AlarmEvents != 1 {
		t.Fatalf("snapshot inventory = %#v", recovery.SnapshotInventory)
	}

	raw, err := os.ReadFile(filepath.Join(target, RecoveryName))
	if err != nil {
		t.Fatalf("recovery record: %v", err)
	}
	var stored Recovery
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !stored.SnapshotAt.Equal(manifest.CreatedAt) || stored.RestoredAt.IsZero() {
		t.Fatalf("stored recovery = %#v", stored)
	}
	if _, err := os.Stat(filepath.Join(target, datadir.Marker)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a restored directory must not claim a running process")
	}
	if _, err := os.Stat(filepath.Join(target, "jetstream", "000001.jetstream")); err != nil {
		t.Fatal("store data was not restored")
	}
	auditBody, err := os.ReadFile(filepath.Join(target, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditBody), "灾难恢复") {
		t.Fatal("restore did not leave an audit record")
	}
}

func TestRestoreMovesThePreviousDataDirectoryAside(t *testing.T) {
	_, archive, _ := createSeed(t)
	target := filepath.Join(t.TempDir(), "data")
	writeFile(t, filepath.Join(target, "old.txt"), "before restore")

	recovery, err := Restore(archive, target)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if recovery.ReplacedDataDir == "" {
		t.Fatal("previous data directory was not named")
	}
	if _, err := os.Stat(filepath.Join(target, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pre-restore data is still in the live directory")
	}
	if _, err := os.Stat(filepath.Join(recovery.ReplacedDataDir, "old.txt")); err != nil {
		t.Fatalf("pre-restore data was not kept aside: %v", err)
	}
}

func TestRestoreRefusesWhileAnInstanceHoldsTheTarget(t *testing.T) {
	_, archive, _ := createSeed(t)
	target := t.TempDir()
	if err := datadir.Write(target, "running"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { datadir.Remove(target) })
	if _, err := Restore(archive, target); !errors.Is(err, ErrInstanceRunning) {
		t.Fatalf("restore while running = %v, want ErrInstanceRunning", err)
	}
}

func TestRestoreRefusesArchiveWithoutManifest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	writeTestArchive(t, archive, []struct{ name, body string }{{"notes.txt", "not a backup"}})
	target := filepath.Join(t.TempDir(), "data")
	if _, err := Restore(archive, target); !errors.Is(err, ErrInvalidArchive) {
		t.Fatalf("restore without manifest = %v, want ErrInvalidArchive", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused restore must not create the data directory")
	}
}

func TestRestoreRefusesArchiveWithAnUnsafePath(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	writeTestArchive(t, archive, []struct{ name, body string }{
		{ManifestName, validManifest(t, time.Now().UTC(), 1)},
		{"../evil.txt", "escape"},
	})
	target := filepath.Join(t.TempDir(), "data")
	_, err := Restore(archive, target)
	if !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("unsafe archive = %v, want ErrUnsafeArchive", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(target), "evil.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("archive escaped the data directory")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused restore must not create the data directory")
	}
}

func TestRestoreRefusesArchiveThatDoesNotMatchItsManifest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	writeTestArchive(t, archive, []struct{ name, body string }{
		{ManifestName, validManifest(t, time.Now().UTC(), 5)},
		{"audit.jsonl", "{}\n"},
	})
	target := filepath.Join(t.TempDir(), "data")
	if _, err := Restore(archive, target); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("incomplete archive = %v, want ErrIncomplete", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused restore must not create the data directory")
	}
}

func TestStatusSeparatesRestoreFromVersionRollback(t *testing.T) {
	dir, archive, manifest := createSeed(t)
	status := ReadStatus(dir)
	if status.Restored || status.Backup == nil || !status.Backup.CreatedAt.Equal(manifest.CreatedAt) {
		t.Fatalf("source status = %#v", status)
	}
	if !status.OfflineOnly || !strings.Contains(status.VersionRollback, "版本回退") {
		t.Fatalf("status must state offline-only and the rollback distinction: %#v", status)
	}

	target := filepath.Join(t.TempDir(), "data")
	if _, err := Restore(archive, target); err != nil {
		t.Fatal(err)
	}
	restored := ReadStatus(target)
	if !restored.Restored || restored.Recovery == nil || !restored.Recovery.SnapshotAt.Equal(manifest.CreatedAt) {
		t.Fatalf("restored status = %#v", restored)
	}
	if restored.Note == "" {
		t.Fatal("restored status must explain what was replaced")
	}
}
