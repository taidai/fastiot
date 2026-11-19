// Package backup creates and restores offline snapshots of a fastiot data
// directory. A snapshot is the whole data directory at one moment: accounts,
// device bindings, history, alarms, audit, project versions and industry
// package state. Restoring replaces the instance with that moment, so the
// report always names the moment and what is not in it.
package backup

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fastiot/internal/audit"
	"fastiot/internal/datadir"
)

const (
	// Format identifies a fastiot data backup archive.
	Format = "fastiot.data-backup"
	// FormatVersion guards the manifest shape.
	FormatVersion = 1
	// ManifestName is the first entry of every archive.
	ManifestName = "manifest.json"
	// RecordName is the local record of the last finished backup.
	RecordName = "backup.json"
	// RecoveryName is written into a restored data directory.
	RecoveryName = "recovery.json"
	// RecoveryFormat identifies the restore record.
	RecoveryFormat = "fastiot.data-recovery"
	// RecordFormat identifies the local backup record.
	RecordFormat = "fastiot.data-backup-record"
)

// VersionRollbackNotice is the product's standing distinction between a
// whole-instance restore and an engineering version rollback.
const VersionRollbackNotice = "版本回退只重放某个工程版本的配置，报警、历史与审计保留；离线灾难恢复用备份整体替换数据目录，快照之后产生的数据不在其中。"

var (
	// ErrInstanceRunning refuses to read or replace a live data directory.
	ErrInstanceRunning = errors.New("instance_running")
	// ErrInvalidArchive means the archive is not a fastiot data backup.
	ErrInvalidArchive = errors.New("invalid_backup")
	// ErrUnsafeArchive refuses entries that escape the data directory.
	ErrUnsafeArchive = errors.New("unsafe_backup_path")
	// ErrIncomplete means the archive does not match its own manifest.
	ErrIncomplete = errors.New("backup_incomplete")
	// ErrDataDirMissing means there is nothing to snapshot.
	ErrDataDirMissing = errors.New("data_dir_missing")
	// ErrUnsupportedEntry refuses data the archive cannot carry faithfully.
	ErrUnsupportedEntry = errors.New("unsupported_data_entry")
)

// Inventory states what the snapshot contains in terms the operator can check
// after a restore.
type Inventory struct {
	AuditRecords     int      `json:"audit_records"`
	AuditFirst       string   `json:"audit_first,omitempty"`
	AuditLast        string   `json:"audit_last,omitempty"`
	AlarmEvents      int      `json:"alarm_events"`
	AlarmFirst       string   `json:"alarm_first,omitempty"`
	AlarmLast        string   `json:"alarm_last,omitempty"`
	ProjectSites     int      `json:"project_sites"`
	ProjectVersions  int      `json:"project_versions"`
	IndustryPackages []string `json:"industry_packages,omitempty"`
	HasInitialSecret bool     `json:"has_initial_password"`
}

// Manifest travels inside the archive and is shown before a restore.
type Manifest struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	AppVersion    string    `json:"app_version,omitempty"`
	DataDir       string    `json:"data_dir"`
	Files         int       `json:"files"`
	Bytes         int64     `json:"bytes"`
	Inventory     Inventory `json:"inventory"`
}

// Record is the local note that a backup finished, so the running instance can
// report the real last snapshot time instead of inventing one.
type Record struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"format_version"`
	CreatedAt     time.Time `json:"created_at"`
	Archive       string    `json:"archive"`
	Files         int       `json:"files"`
	Bytes         int64     `json:"bytes"`
	Inventory     Inventory `json:"inventory"`
}

// DataLoss names the window the restore cannot contain.
type DataLoss struct {
	After  time.Time `json:"after"`
	Before time.Time `json:"before"`
	Detail string    `json:"detail"`
}

// Recovery is the record written into a restored data directory and returned
// to the operator who ran the restore.
type Recovery struct {
	Format             string    `json:"format"`
	FormatVersion      int       `json:"format_version"`
	RestoredAt         time.Time `json:"restored_at"`
	SnapshotAt         time.Time `json:"snapshot_at"`
	BackupAppVersion   string    `json:"backup_app_version,omitempty"`
	SnapshotInventory  Inventory `json:"snapshot_inventory"`
	DataLoss           DataLoss  `json:"data_loss"`
	ReplacedDataDir    string    `json:"replaced_data_dir,omitempty"`
	Includes           []string  `json:"includes"`
	NotVersionRollback string    `json:"not_version_rollback"`
}

// Status is what a running instance reports about its own backup history.
type Status struct {
	Restored        bool      `json:"restored"`
	Recovery        *Recovery `json:"recovery,omitempty"`
	Backup          *Record   `json:"backup,omitempty"`
	OfflineOnly     bool      `json:"offline_only"`
	VersionRollback string    `json:"version_rollback"`
	Note            string    `json:"note,omitempty"`
}

type archiveFile struct {
	rel  string
	mode os.FileMode
	size int64
}

// Create snapshots dataDir into archivePath and records the snapshot locally.
func Create(dataDir, archivePath, appVersion string) (Manifest, error) {
	if dataDir == "" || archivePath == "" {
		return Manifest{}, ErrDataDirMissing
	}
	if err := stopped(dataDir); err != nil {
		return Manifest{}, err
	}
	absDir, err := filepath.Abs(dataDir)
	if err != nil {
		return Manifest{}, err
	}
	info, err := os.Stat(absDir)
	if err != nil || !info.IsDir() {
		return Manifest{}, ErrDataDirMissing
	}
	absOut, err := filepath.Abs(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	files, total, err := collectFiles(absDir, absOut)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Format: Format, FormatVersion: FormatVersion,
		CreatedAt: time.Now().UTC(), AppVersion: appVersion,
		DataDir: filepath.Base(absDir), Files: len(files), Bytes: total,
		Inventory: inventoryOf(absDir),
	}
	if err := writeArchive(absDir, absOut, manifest, files); err != nil {
		return Manifest{}, err
	}
	record := Record{
		Format: RecordFormat, FormatVersion: FormatVersion,
		CreatedAt: manifest.CreatedAt, Archive: filepath.Base(absOut),
		Files: manifest.Files, Bytes: manifest.Bytes, Inventory: manifest.Inventory,
	}
	// The archive is the deliverable; a failed local note only means the
	// running instance keeps saying it has no backup record.
	writeJSONFile(filepath.Join(absDir, RecordName), record)
	return manifest, nil
}

// ReadManifest opens an archive and returns only its manifest.
func ReadManifest(archivePath string) (Manifest, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	reader := tar.NewReader(gz)
	header, err := reader.Next()
	if err != nil || header.Typeflag != tar.TypeReg || header.Name != ManifestName {
		return Manifest{}, ErrInvalidArchive
	}
	var manifest Manifest
	if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	if manifest.Format != Format || manifest.FormatVersion != FormatVersion {
		return Manifest{}, ErrInvalidArchive
	}
	return manifest, nil
}

// Restore replaces dataDir with the archive contents and records the moment
// and the data loss window.
func Restore(archivePath, dataDir string) (Recovery, error) {
	if dataDir == "" {
		return Recovery{}, ErrDataDirMissing
	}
	if err := stopped(dataDir); err != nil {
		return Recovery{}, err
	}
	target := filepath.Clean(dataDir)
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return Recovery{}, err
	}
	staging, err := os.MkdirTemp(parent, filepath.Base(target)+".restore-*")
	if err != nil {
		return Recovery{}, err
	}
	staged := true
	defer func() {
		if staged {
			_ = os.RemoveAll(staging)
		}
	}()
	manifest, err := extract(archivePath, staging)
	if err != nil {
		return Recovery{}, err
	}
	_ = os.Remove(filepath.Join(staging, datadir.Marker))

	aside := ""
	if _, err := os.Stat(target); err == nil {
		aside = uniquePath(target + ".pre-restore-" + time.Now().UTC().Format("20060102-150405"))
	} else if !errors.Is(err, os.ErrNotExist) {
		return Recovery{}, err
	}

	// The record goes into the staged directory, so a data directory only
	// ever carries a recovery record for a swap that actually happened.
	restoredAt := time.Now().UTC()
	recovery := Recovery{
		Format: RecoveryFormat, FormatVersion: FormatVersion,
		RestoredAt: restoredAt, SnapshotAt: manifest.CreatedAt,
		BackupAppVersion: manifest.AppVersion, SnapshotInventory: manifest.Inventory,
		DataLoss: DataLoss{
			After: manifest.CreatedAt, Before: restoredAt,
			Detail: "快照时间之后到恢复时刻之间产生的历史、报警、审计与工程变更不在本次恢复的实例中；恢复不合并这些数据，也不回放旧命令。",
		},
		ReplacedDataDir:    aside,
		Includes:           []string{"账号与登录凭据", "设备接入与工程配置", "历史数据", "报警记录", "审计记录", "项目版本文件", "行业包安装状态"},
		NotVersionRollback: VersionRollbackNotice,
	}
	if err := writeJSONFile(filepath.Join(staging, RecoveryName), recovery); err != nil {
		return Recovery{}, err
	}
	if aside != "" {
		if err := os.Rename(target, aside); err != nil {
			return Recovery{}, fmt.Errorf("move previous data directory aside: %w", err)
		}
	}
	if err := os.Rename(staging, target); err != nil {
		if aside != "" {
			_ = os.Rename(aside, target)
		}
		return Recovery{}, fmt.Errorf("install restored data directory: %w", err)
	}
	staged = false

	store := audit.New(filepath.Join(target, "audit.jsonl"))
	detail := fmt.Sprintf("快照 %s · 恢复 %s · 快照之后的数据不在本次恢复中", stamp(recovery.SnapshotAt), stamp(recovery.RestoredAt))
	if aside != "" {
		detail += " · 原数据目录保留于 " + filepath.Base(aside)
	}
	_ = store.Append(audit.Record{
		Time: restoredAt, Actor: "离线恢复", Action: "灾难恢复",
		Object: filepath.Base(archivePath), Detail: detail, Result: "success",
	})
	return recovery, nil
}

// ReadStatus reads the local backup and recovery records of a data directory.
func ReadStatus(dataDir string) Status {
	status := Status{OfflineOnly: true, VersionRollback: VersionRollbackNotice}
	if record, err := readRecovery(dataDir); err == nil {
		status.Restored = true
		status.Recovery = &record
		status.Note = fmt.Sprintf("本实例于 %s 从 %s 的备份恢复；快照之后到恢复之前产生的数据不在其中。",
			stamp(record.RestoredAt), stamp(record.SnapshotAt))
	}
	if record, err := readRecord(dataDir); err == nil {
		status.Backup = &record
	}
	if !status.Restored && status.Backup == nil {
		status.Note = "本实例没有恢复记录；离线备份与恢复都必须停机执行，应用不虚构最近备份时间。"
	}
	return status
}

func readRecovery(dataDir string) (Recovery, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, RecoveryName))
	if err != nil {
		return Recovery{}, err
	}
	var record Recovery
	if err := json.Unmarshal(raw, &record); err != nil {
		return Recovery{}, err
	}
	if record.Format != RecoveryFormat || record.FormatVersion != FormatVersion || record.RestoredAt.IsZero() {
		return Recovery{}, ErrInvalidArchive
	}
	return record, nil
}

func readRecord(dataDir string) (Record, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, RecordName))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, err
	}
	if record.Format != RecordFormat || record.FormatVersion != FormatVersion || record.CreatedAt.IsZero() {
		return Record{}, ErrInvalidArchive
	}
	return record, nil
}

func collectFiles(dataDir, archivePath string) ([]archiveFile, int64, error) {
	skip := map[string]bool{archivePath: true, archivePath + ".tmp": true}
	var files []archiveFile
	var total int64
	err := filepath.WalkDir(dataDir, func(current string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if current == filepath.Join(dataDir, datadir.Marker) || skip[current] {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symbolic link", ErrUnsupportedEntry, current)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: %s is not a regular file", ErrUnsupportedEntry, current)
		}
		rel, err := filepath.Rel(dataDir, current)
		if err != nil {
			return err
		}
		files = append(files, archiveFile{rel: filepath.ToSlash(rel), mode: info.Mode().Perm(), size: info.Size()})
		total += info.Size()
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	return files, total, nil
}

func writeArchive(dataDir, archivePath string, manifest Manifest, files []archiveFile) error {
	if err := os.MkdirAll(filepath.Dir(archivePath), 0700); err != nil {
		return err
	}
	tmp := archivePath + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	fail := func(err error) error {
		_ = writer.Close()
		_ = gz.Close()
		_ = file.Close()
		_ = os.Remove(tmp)
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := writer.WriteHeader(&tar.Header{
		Name: ManifestName, Mode: 0600, Size: int64(len(encoded)),
		Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt,
	}); err != nil {
		return fail(err)
	}
	if _, err := writer.Write(encoded); err != nil {
		return fail(err)
	}
	for _, entry := range files {
		source, err := os.Open(filepath.Join(dataDir, filepath.FromSlash(entry.rel)))
		if err != nil {
			return fail(err)
		}
		header := &tar.Header{
			Name: entry.rel, Mode: int64(entry.mode), Size: entry.size,
			Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt,
		}
		if err := writer.WriteHeader(header); err != nil {
			source.Close()
			return fail(err)
		}
		if _, err := io.Copy(writer, source); err != nil {
			source.Close()
			return fail(err)
		}
		source.Close()
	}
	if err := writer.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	if err := file.Close(); err != nil {
		return fail(err)
	}
	return os.Rename(tmp, archivePath)
}

func extract(archivePath, staging string) (Manifest, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	reader := tar.NewReader(gz)
	header, err := reader.Next()
	if err != nil || header.Typeflag != tar.TypeReg || header.Name != ManifestName {
		return Manifest{}, ErrInvalidArchive
	}
	var manifest Manifest
	if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
		return Manifest{}, ErrInvalidArchive
	}
	if manifest.Format != Format || manifest.FormatVersion != FormatVersion {
		return Manifest{}, ErrInvalidArchive
	}
	files := 0
	var bytes int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, ErrInvalidArchive
		}
		if header.Typeflag == tar.TypeDir {
			if _, err := safePath(header.Name, staging); err != nil {
				return Manifest{}, err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return Manifest{}, ErrUnsafeArchive
		}
		rel, err := safePath(header.Name, staging)
		if err != nil {
			return Manifest{}, err
		}
		if rel == ManifestName {
			return Manifest{}, ErrInvalidArchive
		}
		destination := filepath.Join(staging, rel)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return Manifest{}, err
		}
		out, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if err != nil {
			return Manifest{}, err
		}
		written, err := io.Copy(out, reader)
		out.Close()
		if err != nil {
			return Manifest{}, err
		}
		files++
		bytes += written
	}
	if files != manifest.Files || bytes != manifest.Bytes {
		return Manifest{}, ErrIncomplete
	}
	return manifest, nil
}

func safePath(name, staging string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\\:") {
		return "", ErrUnsafeArchive
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || path.IsAbs(cleaned) || strings.HasPrefix(cleaned, "../") {
		return "", ErrUnsafeArchive
	}
	rel := filepath.FromSlash(cleaned)
	full := filepath.Join(staging, rel)
	if !strings.HasPrefix(full, filepath.Clean(staging)+string(os.PathSeparator)) {
		return "", ErrUnsafeArchive
	}
	return rel, nil
}

func inventoryOf(dataDir string) Inventory {
	inventory := Inventory{}
	inventory.AuditRecords, inventory.AuditFirst, inventory.AuditLast = jsonlStats(dataDir, "audit.jsonl")
	inventory.AlarmEvents, inventory.AlarmFirst, inventory.AlarmLast = jsonlStats(dataDir, "alarm-events.jsonl")
	inventory.ProjectSites, inventory.ProjectVersions = projectStats(filepath.Join(dataDir, "projects"))
	inventory.IndustryPackages = industryPackages(filepath.Join(dataDir, "industry-packages.json"))
	if _, err := os.Stat(filepath.Join(dataDir, "initial-password.txt")); err == nil {
		inventory.HasInitialSecret = true
	}
	return inventory
}

// jsonlStats counts records and the time range of an append-only log and its
// rotated siblings. Counts include unparseable lines so the total still
// matches what the operator sees in the files.
func jsonlStats(dataDir, name string) (int, string, string) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return 0, "", ""
	}
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Name() == name || strings.HasPrefix(entry.Name(), name+".") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	count := 0
	var first, last time.Time
	for _, file := range names {
		handle, err := os.Open(filepath.Join(dataDir, file))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(handle)
		scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			count++
			var record struct {
				Time time.Time `json:"time"`
			}
			if json.Unmarshal([]byte(line), &record) != nil || record.Time.IsZero() {
				continue
			}
			if first.IsZero() || record.Time.Before(first) {
				first = record.Time
			}
			if last.IsZero() || record.Time.After(last) {
				last = record.Time
			}
		}
		handle.Close()
	}
	return count, stamp(first), stamp(last)
}

func projectStats(root string) (int, int) {
	sites, versions := 0, 0
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0
	}
	for _, site := range entries {
		if !site.IsDir() {
			continue
		}
		sites++
		files, err := os.ReadDir(filepath.Join(root, site.Name()))
		if err != nil {
			continue
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
				continue
			}
			versions++
		}
	}
	return sites, versions
}

func industryPackages(statePath string) []string {
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return nil
	}
	var state struct {
		Installed map[string]struct {
			Version string `json:"version"`
		} `json:"installed"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	packages := make([]string, 0, len(state.Installed))
	for id, record := range state.Installed {
		packages = append(packages, id+"@"+record.Version)
	}
	sort.Strings(packages)
	return packages
}

func writeJSONFile(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func uniquePath(base string) string {
	candidate := base
	for index := 2; ; index++ {
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, index)
	}
}

// stopped maps the ownership check into this package's refusal name.
func stopped(dataDir string) error {
	err := datadir.CheckStopped(dataDir)
	if errors.Is(err, datadir.ErrRunning) {
		return ErrInstanceRunning
	}
	return err
}

func stamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}
