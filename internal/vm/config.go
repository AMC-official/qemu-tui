// Package vm manages VM definitions and QEMU processes.
package vm

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	nameRE  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
	macRE   = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}$`)
	ifaceRE = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)
	usbRE   = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$`)
)

// Choice lists used by validation and the UI.
var (
	Architectures = []string{"x86_64", "aarch64", "i386", "riscv64"}
	Displays      = []string{"gtk", "sdl", "vnc", "spice"}
	Firmwares     = []string{"bios", "uefi"}
	NetModes      = []string{"user", "tap", "none"}
	DiskFormats   = []string{"qcow2", "raw"}
	BootDevices   = []string{"disk", "cdrom", "network"}
)

// Config is the per-VM config.json.
type Config struct {
	Name         string   `json:"name"`
	Memory       int      `json:"memory"` // MiB
	CPUs         int      `json:"cpus"`
	Architecture string   `json:"architecture"`
	Disk         string   `json:"disk"` // file inside disks/
	DiskFormat   string   `json:"disk_format,omitempty"`
	DiskSize     string   `json:"disk_size,omitempty"`
	DiskBus      string   `json:"disk_bus,omitempty"` // virtio (default) or ide
	ExtraDisks   []string `json:"extra_disks,omitempty"`
	ISO          string   `json:"iso,omitempty"`
	Firmware     string   `json:"firmware"`
	Network      bool     `json:"network"`
	NetworkMode  string   `json:"network_mode,omitempty"`
	TapIface     string   `json:"tap_iface,omitempty"`
	MAC          string   `json:"mac,omitempty"`
	Display      string   `json:"display"`
	Boot         string   `json:"boot,omitempty"`
	Serial       bool     `json:"serial,omitempty"`
	USB          []string `json:"usb,omitempty"` // vendor:product
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// IsX86 reports whether the guest is x86.
func (c Config) IsX86() bool { return c.Architecture == "x86_64" || c.Architecture == "i386" }

// ApplyDefaults fills unset fields.
func (c *Config) ApplyDefaults() {
	if c.Memory == 0 {
		c.Memory = 2048
	}
	if c.CPUs == 0 {
		c.CPUs = 2
	}
	if c.Architecture == "" {
		c.Architecture = "x86_64"
	}
	if c.DiskFormat == "" {
		c.DiskFormat = "qcow2"
		if strings.HasSuffix(c.Disk, ".raw") || strings.HasSuffix(c.Disk, ".img") {
			c.DiskFormat = "raw"
		}
	}
	if c.Disk == "" {
		c.Disk = "main." + c.DiskFormat
	}
	if c.Firmware == "" {
		c.Firmware = "bios"
		if !c.IsX86() {
			c.Firmware = "uefi"
		}
	}
	if c.NetworkMode == "" {
		c.NetworkMode = "none"
		if c.Network {
			c.NetworkMode = "user"
		}
	}
	c.Network = c.NetworkMode != "none"
	if c.Display == "" {
		c.Display = "gtk"
	}
	if c.Boot == "" {
		c.Boot = "disk"
	}
	if c.DiskBus == "" {
		c.DiskBus = "virtio"
	}
}

// Validate checks every field. Nothing from a config reaches a shell.
func (c Config) Validate() error {
	if !ValidName(c.Name) {
		return fmt.Errorf("invalid VM name %q (letters, digits, . _ - ; max 63)", c.Name)
	}
	if c.Memory < 16 || c.Memory > 4<<20 {
		return fmt.Errorf("memory %d MiB out of range", c.Memory)
	}
	if c.CPUs < 1 || c.CPUs > 512 {
		return fmt.Errorf("cpus %d out of range", c.CPUs)
	}
	if !oneOf(c.Architecture, Architectures) {
		return fmt.Errorf("unsupported architecture %q", c.Architecture)
	}
	if err := ValidateBaseName(c.Disk); err != nil {
		return err
	}
	for _, d := range c.ExtraDisks {
		if err := ValidateBaseName(d); err != nil {
			return err
		}
	}
	if !oneOf(c.DiskFormat, DiskFormats) {
		return fmt.Errorf("unsupported disk format %q", c.DiskFormat)
	}
	if c.DiskBus != "virtio" && !(c.DiskBus == "ide" && c.IsX86()) {
		return fmt.Errorf("disk bus %q not available for %s", c.DiskBus, c.Architecture)
	}
	if !oneOf(c.Firmware, Firmwares) || (c.Firmware == "bios" && !c.IsX86()) {
		return fmt.Errorf("firmware %q not available for %s", c.Firmware, c.Architecture)
	}
	if !oneOf(c.NetworkMode, NetModes) {
		return fmt.Errorf("unsupported network mode %q", c.NetworkMode)
	}
	if c.NetworkMode == "tap" && !ifaceRE.MatchString(c.TapIface) {
		return fmt.Errorf("tap networking needs a valid interface name")
	}
	if c.MAC != "" && !macRE.MatchString(c.MAC) {
		return fmt.Errorf("invalid MAC address %q", c.MAC)
	}
	if !oneOf(c.Display, Displays) {
		return fmt.Errorf("unsupported display %q", c.Display)
	}
	if !oneOf(c.Boot, BootDevices) {
		return fmt.Errorf("unsupported boot device %q", c.Boot)
	}
	if c.ISO != "" {
		if strings.ContainsAny(c.ISO, "\x00\r\n") {
			return fmt.Errorf("invalid ISO path")
		}
		if !filepath.IsAbs(c.ISO) {
			if err := ValidateBaseName(c.ISO); err != nil {
				return err
			}
		}
	}
	for _, u := range c.USB {
		if !usbRE.MatchString(u) {
			return fmt.Errorf("invalid USB id %q (expected vvvv:pppp)", u)
		}
	}
	return nil
}

// ValidName reports whether n is a safe VM name (also a safe directory name).
func ValidName(n string) bool { return nameRE.MatchString(n) }

// ValidateBaseName rejects anything that is not a plain file name.
func ValidateBaseName(s string) error {
	if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "/\\\x00\r\n") {
		return fmt.Errorf("invalid file name %q", s)
	}
	return nil
}

// SafeJoin joins rel under base and refuses anything that escapes base.
func SafeJoin(base, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || strings.ContainsRune(rel, 0) {
		return "", fmt.Errorf("invalid path %q", rel)
	}
	b := filepath.Clean(base)
	full := filepath.Join(b, rel)
	r, err := filepath.Rel(b, full)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %s", rel, base)
	}
	return full, nil
}

// RandomMAC returns a locally administered QEMU-style MAC (52:54:00:xx:xx:xx).
func RandomMAC() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "52:54:00:12:34:56"
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2])
}

// LoadConfig reads, defaults and validates a config.json.
func LoadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	c.ApplyDefaults()
	return c, c.Validate()
}

// SaveConfig writes config.json atomically.
func SaveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
