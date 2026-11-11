package vm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const specConfig = `{
    "name": "debian",
    "memory": 4096,
    "cpus": 4,
    "architecture": "x86_64",
    "disk": "main.qcow2",
    "iso": "debian.iso",
    "firmware": "uefi",
    "network": true,
    "display": "gtk"
}`

func writeVM(t *testing.T, m *Manager, name string) string {
	t.Helper()
	dir := filepath.Join(m.Root, name)
	for _, s := range []string{"disks", "firmware", "snapshots", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, s), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	c := Config{Name: name}
	c.ApplyDefaults()
	if err := SaveConfig(filepath.Join(dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newMgr(t *testing.T) *Manager {
	d := t.TempDir()
	return NewManager(filepath.Join(d, "vms"), filepath.Join(d, "logs"), filepath.Join(d, "isos"))
}

func TestParseSpecConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(specConfig), 0o644)
	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Memory != 4096 || c.CPUs != 4 || c.NetworkMode != "user" || !c.Network || c.Boot != "disk" {
		t.Fatalf("unexpected config: %+v", c)
	}
	// round trip
	if err := SaveConfig(p, c); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadConfig(p)
	if err != nil || c2.Name != "debian" || c2.Firmware != "uefi" {
		t.Fatalf("round trip: %+v %v", c2, err)
	}
}

func TestValidate(t *testing.T) {
	good := Config{Name: "vm1"}
	good.ApplyDefaults()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	mut := map[string]func(*Config){
		"bad name":      func(c *Config) { c.Name = "../evil" },
		"empty name":    func(c *Config) { c.Name = "" },
		"huge ram":      func(c *Config) { c.Memory = 1 << 30 },
		"zero cpu":      func(c *Config) { c.CPUs = -1 },
		"arch":          func(c *Config) { c.Architecture = "mips" },
		"disk path":     func(c *Config) { c.Disk = "../../etc/passwd" },
		"extra disk":    func(c *Config) { c.ExtraDisks = []string{"a/b"} },
		"iso relative":  func(c *Config) { c.ISO = "../x.iso" },
		"bios on arm":   func(c *Config) { c.Architecture, c.Firmware = "aarch64", "bios" },
		"tap no iface":  func(c *Config) { c.NetworkMode = "tap" },
		"mac":           func(c *Config) { c.MAC = "zz" },
		"display":       func(c *Config) { c.Display = "x11; reboot" },
		"usb":           func(c *Config) { c.USB = []string{"1234;rm"} },
		"ide on arm":    func(c *Config) { c.Architecture, c.Firmware, c.DiskBus = "aarch64", "uefi", "ide" },
		"boot":          func(c *Config) { c.Boot = "floppy" },
		"disk format":   func(c *Config) { c.DiskFormat = "vmdk" },
		"iso newline":   func(c *Config) { c.ISO = "/a\nb.iso" },
		"network mode":  func(c *Config) { c.NetworkMode = "bridge" },
	}
	for name, f := range mut {
		c := good
		f(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	base := "/data/vms/debian/disks"
	if p, err := SafeJoin(base, "main.qcow2"); err != nil || p != filepath.Join(base, "main.qcow2") {
		t.Fatalf("%q %v", p, err)
	}
	for _, bad := range []string{"", "..", "../x", "a/../../x", "/etc/passwd", ".", "x\x00y"} {
		if _, err := SafeJoin(base, bad); err == nil {
			t.Errorf("SafeJoin(%q) should fail", bad)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`, "a\x00"} {
		if ValidateBaseName(bad) == nil {
			t.Errorf("ValidateBaseName(%q) should fail", bad)
		}
	}
	if ValidName("-rf") || ValidName("a b") || !ValidName("Debian-12.1") {
		t.Error("ValidName")
	}
}

func TestRandomMAC(t *testing.T) {
	m := RandomMAC()
	if !macRE.MatchString(m) || !strings.HasPrefix(m, "52:54:00:") {
		t.Fatalf("bad mac %s", m)
	}
}

func TestStaleStateCleanup(t *testing.T) {
	m := newMgr(t)
	dir := writeVM(t, m, "ghost")
	sp := filepath.Join(dir, "runtime.json")

	// PID that cannot exist
	if err := writeRunState(sp, runState{PID: 999999999, QMPNet: "unix", QMPAddr: "/nonexistent/x.qmp"}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status("ghost"); st != Stopped {
		t.Fatalf("status = %s", st)
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatal("stale state file must be removed")
	}

	// A live PID (ours) that is NOT a qemu process must never be treated as the VM.
	if err := writeRunState(sp, runState{PID: os.Getpid(), QMPNet: "unix", QMPAddr: "/nonexistent/y.qmp"}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status("ghost"); st != Stopped {
		t.Fatalf("foreign pid treated as running: %s", st)
	}
	rs := runState{PID: os.Getpid(), QMPNet: "unix", QMPAddr: "/nonexistent/y.qmp"}
	if err := rs.kill(); err == nil {
		t.Fatal("kill must refuse a process that is not this VM's QEMU")
	}

	// corrupt state file
	os.WriteFile(sp, []byte("{nope"), 0o600)
	if st := m.Status("ghost"); st != Stopped {
		t.Fatal("corrupt state must read as stopped")
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatal("corrupt state must be removed")
	}
	if lines, _ := m.GlobalLog(10); len(lines) == 0 {
		t.Error("cleanup should be logged")
	}
}

func TestDuplicateStartAndNotRunningErrors(t *testing.T) {
	m := newMgr(t)
	writeVM(t, m, "a")
	for name, f := range map[string]func() error{
		"stop":    func() error { return m.Stop("a") },
		"force":   func() error { return m.ForceStop("a") },
		"pause":   func() error { return m.Pause("a") },
		"restart": func() error { return m.Restart("a") },
	} {
		if err := f(); err == nil || !strings.Contains(err.Error(), "not running") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if err := m.Start("../a"); err == nil {
		t.Error("traversal name must be rejected")
	}
}

func TestListAndNames(t *testing.T) {
	m := newMgr(t)
	writeVM(t, m, "zeta")
	writeVM(t, m, "Alpha")
	os.MkdirAll(filepath.Join(m.Root, "not-a-vm"), 0o755)
	names := m.Names()
	if len(names) != 2 || names[0] != "Alpha" || names[1] != "zeta" {
		t.Fatalf("names: %v", names)
	}
	infos, _ := m.List()
	if len(infos) != 2 || infos[0].State != Stopped {
		t.Fatalf("infos: %+v", infos)
	}
}

func TestDeleteSafety(t *testing.T) {
	m := newMgr(t)
	os.MkdirAll(filepath.Join(m.Root, "nocfg"), 0o755)
	if err := m.Delete("nocfg"); err == nil {
		t.Error("must refuse dir without config.json")
	}
	if err := m.Delete("../x"); err == nil {
		t.Error("must refuse traversal")
	}
	writeVM(t, m, "gone")
	if err := m.Delete("gone"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(m.Root, "gone")); !os.IsNotExist(err) {
		t.Error("vm dir should be gone")
	}
}

func TestISOHandling(t *testing.T) {
	m := newMgr(t)
	writeVM(t, m, "a")
	os.MkdirAll(m.ISODir, 0o755)
	os.WriteFile(filepath.Join(m.ISODir, "x.iso"), []byte("iso"), 0o644)
	if got := m.ListISOs(); len(got) != 1 || got[0] != "x.iso" {
		t.Fatalf("isos: %v", got)
	}
	if err := m.AttachISO("a", "x.iso"); err != nil {
		t.Fatal(err)
	}
	c, _ := m.Load("a")
	if c.ISO != "x.iso" {
		t.Fatalf("iso = %q", c.ISO)
	}
	if err := m.AttachISO("a", "missing.iso"); err == nil {
		t.Error("missing ISO must fail")
	}
	if err := m.AttachISO("a", "../x.iso"); err == nil {
		t.Error("traversal ISO must fail")
	}
	if err := m.EjectISO("a"); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Load("a"); c.ISO != "" {
		t.Error("iso should be cleared")
	}
}

func TestUSBConfig(t *testing.T) {
	m := newMgr(t)
	writeVM(t, m, "a")
	if err := m.USBAttach("a", "046D:C534"); err != nil {
		t.Fatal(err)
	}
	m.USBAttach("a", "046d:c534") // duplicate, case-insensitive
	if c, _ := m.Load("a"); len(c.USB) != 1 {
		t.Fatalf("usb: %v", c.USB)
	}
	if err := m.USBAttach("a", "bogus"); err == nil {
		t.Error("bad id must fail")
	}
	if err := m.USBRemove("a", "046d:c534"); err != nil {
		t.Fatal(err)
	}
	if c, _ := m.Load("a"); len(c.USB) != 0 {
		t.Fatalf("usb not removed: %v", c.USB)
	}
}

func TestCreateCloneDeleteWithQemuImg(t *testing.T) {
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not installed")
	}
	m := newMgr(t)
	if err := m.Create(Config{Name: "d1", DiskSize: "32M", NetworkMode: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Create(Config{Name: "d1"}); err == nil {
		t.Error("duplicate create must fail")
	}
	if err := m.Create(Config{Name: "bad/name"}); err == nil {
		t.Error("bad name must fail")
	}
	if err := m.CreateSnapshot("d1", "clean"); err != nil {
		t.Fatal(err)
	}
	if err := m.Clone("d1", "d2"); err != nil {
		t.Fatal(err)
	}
	a, _ := m.Load("d1")
	b, _ := m.Load("d2")
	if a.MAC == b.MAC || b.Name != "d2" {
		t.Fatalf("clone must get a new MAC and name: %+v %+v", a, b)
	}
	snaps, err := m.Snapshots("d2")
	if err != nil || len(snaps) != 1 {
		t.Fatalf("clone snapshots: %v %v", snaps, err)
	}
	if err := m.Delete("d1"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete("d2"); err != nil {
		t.Fatal(err)
	}
}
