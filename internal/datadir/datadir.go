// Package datadir records which process currently owns a fastiot data
// directory, so offline backup and restore can refuse to touch a live
// instance. It is not a lock: it is an honest claim of ownership that survives
// a crash as a stale marker.
package datadir

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// Marker is the runtime claim file kept inside the data directory. It is
// runtime state, not data, and never enters a backup archive.
const Marker = "instance.json"

var (
	// ErrRunning means a live process holds the data directory.
	ErrRunning = errors.New("instance_running")
	// ErrMarkerInvalid means the marker exists but cannot be read, so this
	// tool cannot prove the directory is stopped.
	ErrMarkerInvalid = errors.New("instance_marker_invalid")
)

// Record is the ownership claim.
type Record struct {
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version,omitempty"`
}

// Write claims the directory for this process.
func Write(dir, version string) error {
	if dir == "" {
		return errors.New("data directory is required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	record := Record{PID: os.Getpid(), StartedAt: time.Now().UTC(), Version: version}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, Marker+".tmp")
	if err := os.WriteFile(tmp, append(encoded, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, Marker))
}

// Remove releases the claim, but only when this process still owns it.
func Remove(dir string) {
	record, err := Read(dir)
	if err != nil || record.PID != os.Getpid() {
		return
	}
	_ = os.Remove(filepath.Join(dir, Marker))
}

// Read returns the recorded claim.
func Read(dir string) (Record, error) {
	raw, err := os.ReadFile(filepath.Join(dir, Marker))
	if err != nil {
		return Record{}, err
	}
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, err
	}
	if record.PID <= 0 {
		return Record{}, fmt.Errorf("marker has no process id")
	}
	return record, nil
}

// Live reports whether the claim names a process that is still running.
func Live(dir string) (Record, bool, error) {
	record, err := Read(dir)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, ErrMarkerInvalid
	}
	return record, processAlive(record.PID), nil
}

// CheckStopped returns nil only when no live process claims the directory.
func CheckStopped(dir string) error {
	_, live, err := Live(dir)
	if err != nil {
		return err
	}
	if live {
		return ErrRunning
	}
	return nil
}

func processAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	defer process.Release()
	if runtime.GOOS == "windows" {
		// FindProcess opens a handle on Windows; success means the process exists.
		return true
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
