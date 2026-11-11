// Package storage wraps qemu-img for disk and snapshot management.
package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var (
	sizeRE     = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[KMGTkmgt]?$`)
	resizeRE   = regexp.MustCompile(`^\+?[0-9]+(\.[0-9]+)?[KMGTkmgt]?$`)
	validFmts  = map[string]bool{"qcow2": true, "raw": true}
	qemuImgBin = "qemu-img"
)

// Disk describes the output of `qemu-img info`.
type Disk struct {
	Filename    string
	Format      string
	VirtualSize int64
	ActualSize  int64
	Snapshots   []Snapshot
}

type rawInfo struct {
	Filename    string     `json:"filename"`
	Format      string     `json:"format"`
	VirtualSize int64      `json:"virtual-size"`
	ActualSize  int64      `json:"actual-size"`
	Snapshots   []Snapshot `json:"snapshots"`
}

// ParseInfo parses `qemu-img info --output=json` output.
func ParseInfo(data []byte) (Disk, error) {
	var r rawInfo
	if err := json.Unmarshal(data, &r); err != nil {
		return Disk{}, fmt.Errorf("parse qemu-img info: %w", err)
	}
	return Disk{Filename: r.Filename, Format: r.Format, VirtualSize: r.VirtualSize,
		ActualSize: r.ActualSize, Snapshots: r.Snapshots}, nil
}

// run executes qemu-img with an argument vector (never a shell string).
func run(args ...string) ([]byte, error) {
	path, err := exec.LookPath(qemuImgBin)
	if err != nil {
		return nil, errors.New("qemu-img not found in PATH")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(path, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("qemu-img %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ValidSize reports whether s looks like a qemu-img size such as 20G.
func ValidSize(s string) bool { return sizeRE.MatchString(s) }

// CreateDisk creates a new image. It refuses to overwrite existing files.
func CreateDisk(path, format, size string) error {
	if !validFmts[format] {
		return fmt.Errorf("unsupported disk format %q", format)
	}
	if !ValidSize(size) {
		return fmt.Errorf("invalid disk size %q (example: 20G)", size)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	_, err := run("create", "-f", format, path, size)
	return err
}

// Info returns image information (uses -U so it also works on busy images).
func Info(path string) (Disk, error) {
	out, err := run("info", "-U", "--output=json", path)
	if err != nil {
		return Disk{}, err
	}
	return ParseInfo(out)
}

// Resize grows an image. Only growth ("20G" absolute or "+5G") is allowed.
func Resize(path, size string) error {
	if !resizeRE.MatchString(size) {
		return fmt.Errorf("invalid size %q (growing only, e.g. 40G or +10G)", size)
	}
	_, err := run("resize", path, size)
	return err
}
