package storage

import (
	"fmt"
	"regexp"
	"time"
)

var snapNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Snapshot is an internal qcow2 snapshot.
type Snapshot struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	VMStateSize int64  `json:"vm-state-size"`
	DateSec     int64  `json:"date-sec"`
	DateNsec    int64  `json:"date-nsec"`
}

// Created returns the creation time.
func (s Snapshot) Created() time.Time { return time.Unix(s.DateSec, s.DateNsec) }

// ValidSnapshotName checks a snapshot tag.
func ValidSnapshotName(n string) bool { return snapNameRE.MatchString(n) }

func requireQcow2(path string) error {
	d, err := Info(path)
	if err != nil {
		return err
	}
	if d.Format != "qcow2" {
		return fmt.Errorf("snapshots need a qcow2 disk (this one is %s)", d.Format)
	}
	return nil
}

// ListSnapshots returns the snapshots of a qcow2 image.
func ListSnapshots(path string) ([]Snapshot, error) {
	d, err := Info(path)
	if err != nil {
		return nil, err
	}
	return d.Snapshots, nil
}

// CreateSnapshot creates an internal snapshot. The VM must be stopped.
func CreateSnapshot(path, name string) error {
	if !ValidSnapshotName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	if err := requireQcow2(path); err != nil {
		return err
	}
	_, err := run("snapshot", "-c", name, path)
	return err
}

// RestoreSnapshot reverts the image to a snapshot. The VM must be stopped.
func RestoreSnapshot(path, name string) error {
	if !ValidSnapshotName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	_, err := run("snapshot", "-a", name, path)
	return err
}

// DeleteSnapshot removes a snapshot.
func DeleteSnapshot(path, name string) error {
	if !ValidSnapshotName(name) {
		return fmt.Errorf("invalid snapshot name %q", name)
	}
	_, err := run("snapshot", "-d", name, path)
	return err
}
