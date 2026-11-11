package storage

import (
	"os/exec"
	"path/filepath"
	"testing"
)

const sampleInfo = `{
  "virtual-size": 21474836480, "filename": "main.qcow2", "format": "qcow2",
  "actual-size": 200704,
  "snapshots": [
    {"id": "1", "name": "clean-install", "vm-state-size": 0, "date-sec": 1783641600, "date-nsec": 0}
  ]
}`

func TestParseInfo(t *testing.T) {
	d, err := ParseInfo([]byte(sampleInfo))
	if err != nil {
		t.Fatal(err)
	}
	if d.Format != "qcow2" || d.VirtualSize != 21474836480 {
		t.Fatalf("bad disk: %+v", d)
	}
	if len(d.Snapshots) != 1 || d.Snapshots[0].Name != "clean-install" {
		t.Fatalf("bad snapshots: %+v", d.Snapshots)
	}
	if d.Snapshots[0].Created().Unix() != 1783641600 {
		t.Fatal("bad snapshot time")
	}
	if _, err := ParseInfo([]byte("nope")); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidation(t *testing.T) {
	for _, ok := range []string{"20G", "512M", "1.5T", "100"} {
		if !ValidSize(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-1G", "20GB", "1;rm", "G"} {
		if ValidSize(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	for _, ok := range []string{"clean-install", "v1.0", "a"} {
		if !ValidSnapshotName(ok) {
			t.Errorf("snapshot %q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "../x", "a/b"} {
		if ValidSnapshotName(bad) {
			t.Errorf("snapshot %q should be invalid", bad)
		}
	}
	if err := CreateDisk("/tmp/x", "vmdk", "1G"); err == nil {
		t.Error("vmdk must be rejected")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}
	p := filepath.Join(t.TempDir(), "d.qcow2")
	if err := CreateDisk(p, "qcow2", "64M"); err != nil {
		t.Fatal(err)
	}
	if err := CreateDisk(p, "qcow2", "64M"); err == nil {
		t.Fatal("must not overwrite")
	}
	if err := CreateSnapshot(p, "one"); err != nil {
		t.Fatal(err)
	}
	s, err := ListSnapshots(p)
	if err != nil || len(s) != 1 || s[0].Name != "one" {
		t.Fatalf("snapshots: %v %v", s, err)
	}
	if err := RestoreSnapshot(p, "one"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteSnapshot(p, "one"); err != nil {
		t.Fatal(err)
	}
	if err := Resize(p, "+64M"); err != nil {
		t.Fatal(err)
	}
	raw := filepath.Join(t.TempDir(), "r.raw")
	if err := CreateDisk(raw, "raw", "8M"); err != nil {
		t.Fatal(err)
	}
	if err := CreateSnapshot(raw, "x"); err == nil {
		t.Fatal("raw disks must refuse snapshots")
	}
}
