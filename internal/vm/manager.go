package vm

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AMC-official/qemu-tui/internal/qemu"
	"github.com/AMC-official/qemu-tui/internal/storage"
)

// State is the runtime state of a VM.
type State string

// VM states.
const (
	Stopped State = "Stopped"
	Running State = "Running"
	Paused  State = "Paused"
)

// Info is a VM definition plus its runtime state.
type Info struct {
	Config Config
	State  State
	PID    int
	Uptime time.Duration
}

// Manager owns the VM directory tree.
type Manager struct {
	Root           string // ~/.local/share/qemu-tui/vms
	LogRoot        string // ~/.local/share/qemu-tui/logs
	ISODir         string
	DefaultDisplay string
}

// NewManager creates a Manager.
func NewManager(root, logRoot, isoDir string) *Manager {
	return &Manager{Root: root, LogRoot: logRoot, ISODir: isoDir, DefaultDisplay: "gtk"}
}

func (m *Manager) dir(name string) (string, error) {
	if !ValidName(name) {
		return "", fmt.Errorf("invalid VM name %q", name)
	}
	return filepath.Join(m.Root, name), nil
}

func cfgPath(dir string) string   { return filepath.Join(dir, "config.json") }
func statePath(dir string) string { return filepath.Join(dir, "runtime.json") }
func logPath(dir string) string   { return filepath.Join(dir, "logs", "qemu.log") }

// Load reads a VM's config.
func (m *Manager) Load(name string) (Config, error) {
	dir, err := m.dir(name)
	if err != nil {
		return Config{}, err
	}
	c, err := LoadConfig(cfgPath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("no such VM %q", name)
	}
	if err == nil && c.Name != name {
		return c, fmt.Errorf("config name %q does not match directory %q", c.Name, name)
	}
	return c, err
}

// Update loads a config, applies f, validates and saves it.
func (m *Manager) Update(name string, f func(*Config) error) error {
	c, err := m.Load(name)
	if err != nil {
		return err
	}
	if err := f(&c); err != nil {
		return err
	}
	c.ApplyDefaults()
	if err := c.Validate(); err != nil {
		return err
	}
	dir, _ := m.dir(name)
	return SaveConfig(cfgPath(dir), c)
}

// Names lists VM names (cheap; no state lookup).
func (m *Manager) Names() []string {
	ents, err := os.ReadDir(m.Root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && ValidName(e.Name()) {
			if _, err := os.Stat(cfgPath(filepath.Join(m.Root, e.Name()))); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// List returns all VMs with their state.
func (m *Manager) List() ([]Info, error) {
	var out []Info
	for _, n := range m.Names() {
		in, err := m.Detail(n)
		if err != nil {
			continue // broken config: skipped, still visible via logs/console errors
		}
		out = append(out, in)
	}
	return out, nil
}

// Detail returns one VM's info.
func (m *Manager) Detail(name string) (Info, error) {
	c, err := m.Load(name)
	if err != nil {
		return Info{}, err
	}
	in := Info{Config: c, State: Stopped}
	if st, ok := m.live(name); ok {
		in.PID = st.PID
		in.Uptime = time.Since(st.Started)
		in.State = m.stateOf(st)
	}
	return in, nil
}

func (m *Manager) stateOf(st *runState) State {
	c, err := qemu.Dial(st.QMPNet, st.QMPAddr, 400*time.Millisecond)
	if err != nil {
		return Running
	}
	defer c.Close()
	if _, running, err := c.Status(); err == nil && !running {
		return Paused
	}
	return Running
}

// Status returns the state of a VM, cleaning up stale runtime files.
func (m *Manager) Status(name string) State {
	st, ok := m.live(name)
	if !ok {
		return Stopped
	}
	return m.stateOf(st)
}

// live returns the verified runtime state, or removes stale state.
func (m *Manager) live(name string) (*runState, bool) {
	dir, err := m.dir(name)
	if err != nil {
		return nil, false
	}
	sp := statePath(dir)
	st, err := readRunState(sp)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			os.Remove(sp)
		}
		return nil, false
	}
	if !st.verified() {
		os.Remove(sp)
		removeOwnSocket(st.QMPNet, st.QMPAddr)
		m.event(name, fmt.Sprintf("Cleaned up stale runtime state (pid %d no longer running)", st.PID))
		return nil, false
	}
	return st, true
}

// removeOwnSocket deletes a socket only if it lives in our private runtime dir.
func removeOwnSocket(network, addr string) {
	if network != "unix" || addr == "" {
		return
	}
	if rd, err := runtimeDir(); err == nil && filepath.Dir(addr) == rd {
		os.Remove(addr)
	}
}

func runtimeDir() (string, error) {
	var d string
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		d = filepath.Join(x, "qemu-tui")
	} else {
		d = filepath.Join(os.TempDir(), fmt.Sprintf("qemu-tui-%d", os.Getuid()))
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(d, 0o700); err != nil {
		return "", fmt.Errorf("runtime dir %s is not ours: %w", d, err)
	}
	return d, nil
}

func (m *Manager) event(name, msg string) {
	if dir, err := m.dir(name); err == nil {
		appendLine(logPath(dir), fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg))
	}
	if m.LogRoot != "" {
		os.MkdirAll(m.LogRoot, 0o755)
		appendLine(filepath.Join(m.LogRoot, "events.log"),
			fmt.Sprintf("%s %s: %s", time.Now().Format("2006-01-02 15:04:05"), name, msg))
	}
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil // no log yet is not an error
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	const maxRead = 64 * 1024
	var off int64
	if fi.Size() > maxRead {
		off = fi.Size() - maxRead
	}
	buf := make([]byte, fi.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, err
	}
	text := strings.TrimRight(string(buf), "\n")
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if off > 0 && len(lines) > 1 {
		lines = lines[1:] // first line is probably cut in half
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// Logs returns the last n lines of a VM's log.
func (m *Manager) Logs(name string, n int) ([]string, error) {
	dir, err := m.dir(name)
	if err != nil {
		return nil, err
	}
	return tailLines(logPath(dir), n)
}

// GlobalLog returns the last n lines of the events log.
func (m *Manager) GlobalLog(n int) ([]string, error) {
	return tailLines(filepath.Join(m.LogRoot, "events.log"), n)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func (m *Manager) resolveISO(iso, dir string) (string, error) {
	iso = expandHome(iso)
	if filepath.IsAbs(iso) {
		p := filepath.Clean(iso)
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("ISO not found: %s", p)
		}
		return p, nil
	}
	if err := ValidateBaseName(iso); err != nil {
		return "", err
	}
	for _, base := range []string{m.ISODir, dir} {
		if base == "" {
			continue
		}
		p := filepath.Join(base, iso)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ISO %q not found in %s or the VM directory", iso, m.ISODir)
}

// ListISOs lists *.iso files in the ISO directory.
func (m *Manager) ListISOs() []string {
	ents, _ := os.ReadDir(m.ISODir)
	var out []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".iso") {
			out = append(out, e.Name())
		}
	}
	return out
}

// Create builds a new VM directory, disk, firmware vars and config.
func (m *Manager) Create(cfg Config) error {
	if cfg.Display == "" {
		cfg.Display = m.DefaultDisplay
	}
	cfg.ISO = expandHome(cfg.ISO)
	cfg.ApplyDefaults()
	if cfg.MAC == "" {
		cfg.MAC = RandomMAC()
	}
	if cfg.DiskSize == "" {
		cfg.DiskSize = "20G"
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if !storage.ValidSize(cfg.DiskSize) {
		return fmt.Errorf("invalid disk size %q", cfg.DiskSize)
	}
	dir, _ := m.dir(cfg.Name)
	if _, err := os.Lstat(dir); err == nil {
		return fmt.Errorf("VM %q already exists", cfg.Name)
	}
	if cfg.ISO != "" {
		if _, err := m.resolveISO(cfg.ISO, dir); err != nil {
			return err
		}
	}
	var vars string
	if cfg.Firmware == "uefi" {
		_, v, err := qemu.FindUEFI(cfg.Architecture)
		if err != nil {
			return err
		}
		vars = v
	}
	for _, sub := range []string{"disks", "firmware", "snapshots", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return err
		}
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	if err := storage.CreateDisk(filepath.Join(dir, "disks", cfg.Disk), cfg.DiskFormat, cfg.DiskSize); err != nil {
		return err
	}
	if vars != "" {
		if err := copyFile(vars, filepath.Join(dir, "firmware", "vars.fd")); err != nil {
			return err
		}
	}
	if err := SaveConfig(cfgPath(dir), cfg); err != nil {
		return err
	}
	ok = true
	m.event(cfg.Name, "VM created")
	return nil
}

func (m *Manager) requireStopped(name string) error {
	if st := m.Status(name); st != Stopped {
		return fmt.Errorf("%s is %s; stop it first", name, strings.ToLower(string(st)))
	}
	return nil
}

// Delete removes a stopped VM and all its files.
func (m *Manager) Delete(name string) error {
	dir, err := m.dir(name)
	if err != nil {
		return err
	}
	if err := m.requireStopped(name); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("no such VM %q", name)
	}
	if !fi.IsDir() { // also rejects a symlinked VM directory
		return fmt.Errorf("%s is not a regular VM directory", dir)
	}
	if _, err := os.Stat(cfgPath(dir)); err != nil {
		return fmt.Errorf("%s has no config.json; refusing to delete", dir)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	m.event(name, "VM deleted")
	return nil
}

// Clone copies a stopped VM under a new name with a fresh MAC address.
func (m *Manager) Clone(src, dst string) error {
	if !ValidName(dst) {
		return fmt.Errorf("invalid VM name %q", dst)
	}
	if err := m.requireStopped(src); err != nil {
		return err
	}
	cfg, err := m.Load(src)
	if err != nil {
		return err
	}
	sdir, _ := m.dir(src)
	ddir, _ := m.dir(dst)
	if _, err := os.Lstat(ddir); err == nil {
		return fmt.Errorf("VM %q already exists", dst)
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(ddir)
		}
	}()
	if err := copyTree(sdir, ddir); err != nil {
		return err
	}
	cfg.Name, cfg.MAC = dst, RandomMAC()
	if err := SaveConfig(cfgPath(ddir), cfg); err != nil {
		return err
	}
	ok = true
	m.event(dst, "VM cloned from "+src)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "runtime.json" {
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("refusing to copy symlink %s", p)
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case filepath.Base(filepath.Dir(p)) == "logs":
			return nil // do not copy logs
		}
		return copyFile(p, target)
	})
}

func freePort(start int) int {
	for p := start; p < start+200; p++ {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			l.Close()
			return p
		}
	}
	return 0
}

func ephemeralPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (m *Manager) params(cfg Config, dir string) (qemu.Params, error) {
	disk, err := SafeJoin(filepath.Join(dir, "disks"), cfg.Disk)
	if err != nil {
		return qemu.Params{}, err
	}
	if _, err := os.Stat(disk); err != nil {
		return qemu.Params{}, fmt.Errorf("disk image missing: %s", disk)
	}
	p := qemu.Params{
		Name: cfg.Name, Arch: cfg.Architecture, MemoryMB: cfg.Memory, CPUs: cfg.CPUs,
		Accel:    qemu.DetectAccel(cfg.Architecture),
		DiskPath: disk, DiskFormat: cfg.DiskFormat, DiskBus: cfg.DiskBus,
		Firmware: cfg.Firmware, NetMode: cfg.NetworkMode, TapIface: cfg.TapIface, MAC: cfg.MAC,
		Display: cfg.Display, Boot: cfg.Boot,
	}
	for _, e := range cfg.ExtraDisks {
		ep, err := SafeJoin(filepath.Join(dir, "disks"), e)
		if err != nil {
			return p, err
		}
		d, err := storage.Info(ep)
		if err != nil {
			return p, err
		}
		p.ExtraDisks = append(p.ExtraDisks, qemu.ExtraDisk{Path: ep, Format: d.Format})
	}
	if cfg.ISO != "" {
		if p.ISOPath, err = m.resolveISO(cfg.ISO, dir); err != nil {
			return p, err
		}
	}
	if cfg.Firmware == "uefi" {
		code, _, err := qemu.FindUEFI(cfg.Architecture)
		if err != nil {
			return p, err
		}
		vars := filepath.Join(dir, "firmware", "vars.fd")
		if _, err := os.Stat(vars); err != nil {
			return p, fmt.Errorf("UEFI vars file missing: %s", vars)
		}
		p.UEFICode, p.UEFIVars = code, vars
	}
	for _, u := range cfg.USB {
		v, pr, _ := strings.Cut(u, ":")
		p.USB = append(p.USB, qemu.USBDevice{VendorID: v, ProductID: pr})
	}
	switch cfg.Display {
	case "vnc":
		port := freePort(5900)
		if port == 0 {
			return p, errors.New("no free VNC port")
		}
		p.VNCDisplay = port - 5900
	case "spice":
		if p.SpicePort = freePort(5930); p.SpicePort == 0 {
			return p, errors.New("no free SPICE port")
		}
	}
	return p, nil
}

// Start launches QEMU for a VM. Arguments are passed as an argv slice, never a shell string.
func (m *Manager) Start(name string) error {
	cfg, err := m.Load(name)
	if err != nil {
		return err
	}
	if st := m.Status(name); st != Stopped {
		return fmt.Errorf("%s is already %s", name, strings.ToLower(string(st)))
	}
	dir, _ := m.dir(name)
	p, err := m.params(cfg, dir)
	if err != nil {
		return err
	}
	rd, err := runtimeDir()
	if err != nil {
		return err
	}
	rs := runState{Started: time.Now()}
	if runtimeIsWindows() {
		port, err := ephemeralPort()
		if err != nil {
			return err
		}
		rs.QMPNet, rs.QMPAddr = "tcp", "127.0.0.1:"+strconv.Itoa(port)
	} else {
		rs.QMPNet, rs.QMPAddr = "unix", filepath.Join(rd, name+".qmp")
		if len(rs.QMPAddr) > 100 {
			return fmt.Errorf("socket path too long: %s", rs.QMPAddr)
		}
		os.Remove(rs.QMPAddr)
		if cfg.Serial {
			rs.SerialPath = filepath.Join(rd, name+".serial")
			os.Remove(rs.SerialPath)
			p.SerialPath = rs.SerialPath
		}
	}
	p.QMPNet, p.QMPAddr = rs.QMPNet, rs.QMPAddr

	bin, args, err := qemu.Build(p)
	if err != nil {
		return err
	}
	binPath, err := qemu.LookPath(bin)
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath(dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	m.event(name, "Starting QEMU...")
	if p.Accel == "kvm" {
		m.event(name, "Using KVM acceleration")
	} else {
		m.event(name, "Using "+strings.ToUpper(p.Accel)+" acceleration")
	}
	switch cfg.Display {
	case "vnc":
		m.event(name, fmt.Sprintf("VNC listening on 127.0.0.1:%d", 5900+p.VNCDisplay))
	case "spice":
		m.event(name, fmt.Sprintf("SPICE listening on 127.0.0.1:%d", p.SpicePort))
	}

	cmd := exec.Command(binPath, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		logf.Close()
		m.event(name, "Failed to start: "+err.Error())
		return err
	}
	rs.PID = cmd.Process.Pid
	sp := statePath(dir)
	if err := writeRunState(sp, rs); err != nil {
		cmd.Process.Kill()
		logf.Close()
		return err
	}
	m.event(name, fmt.Sprintf("Boot sequence started (pid %d)", rs.PID))

	done := make(chan int, 1)
	go func() {
		cmd.Wait()
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		m.event(name, fmt.Sprintf("QEMU exited (status %d)", code))
		if cur, err := readRunState(sp); err == nil && cur.PID == rs.PID {
			os.Remove(sp)
		}
		removeOwnSocket(rs.QMPNet, rs.QMPAddr)
		logf.Close()
		done <- code
	}()
	select {
	case code := <-done:
		tail, _ := tailLines(logPath(dir), 8)
		return fmt.Errorf("QEMU exited immediately (status %d):\n%s", code, strings.Join(tail, "\n"))
	case <-time.After(700 * time.Millisecond):
	}
	return nil
}

func (m *Manager) withQMP(name string, f func(*qemu.Client) error) error {
	st, ok := m.live(name)
	if !ok {
		return fmt.Errorf("%s is not running", name)
	}
	c, err := qemu.Dial(st.QMPNet, st.QMPAddr, 2*time.Second)
	if err != nil {
		return fmt.Errorf("cannot reach QEMU monitor: %w", err)
	}
	defer c.Close()
	return f(c)
}

func (m *Manager) simple(name, cmd, msg string) error {
	err := m.withQMP(name, func(c *qemu.Client) error { _, err := c.Execute(cmd, nil); return err })
	if err == nil {
		m.event(name, msg)
	}
	return err
}

// Stop sends an ACPI power-down request (graceful).
func (m *Manager) Stop(name string) error {
	return m.simple(name, "system_powerdown", "ACPI power-down requested")
}

// Restart resets the guest.
func (m *Manager) Restart(name string) error { return m.simple(name, "system_reset", "Guest reset") }

// Pause freezes the guest.
func (m *Manager) Pause(name string) error { return m.simple(name, "stop", "Paused") }

// Resume continues a paused guest.
func (m *Manager) Resume(name string) error { return m.simple(name, "cont", "Resumed") }

// ForceStop quits QEMU immediately; falls back to a verified kill of OUR process only.
func (m *Manager) ForceStop(name string) error {
	st, ok := m.live(name)
	if !ok {
		return fmt.Errorf("%s is not running", name)
	}
	err := m.withQMP(name, func(c *qemu.Client) error { _, err := c.Execute("quit", nil); return err })
	if err != nil {
		if kerr := st.kill(); kerr != nil {
			return fmt.Errorf("%v; %v", err, kerr)
		}
	}
	for i := 0; i < 50 && pidAlive(st.PID); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if pidAlive(st.PID) {
		if err := st.kill(); err != nil {
			return err
		}
	}
	if dir, err := m.dir(name); err == nil {
		os.Remove(statePath(dir))
	}
	removeOwnSocket(st.QMPNet, st.QMPAddr)
	m.event(name, "Force stopped")
	return nil
}

// Monitor runs a human-monitor command ("info status") and returns its output.
func (m *Manager) Monitor(name, line string) (string, error) {
	var out string
	err := m.withQMP(name, func(c *qemu.Client) error {
		var e error
		out, e = c.HumanCommand(line)
		return e
	})
	return out, err
}

// SerialPath returns the serial socket of a running VM.
func (m *Manager) SerialPath(name string) (string, error) {
	cfg, err := m.Load(name)
	if err != nil {
		return "", err
	}
	if !cfg.Serial {
		return "", errors.New("serial console is disabled for this VM (set \"serial\": true in its config)")
	}
	st, ok := m.live(name)
	if !ok || st.SerialPath == "" {
		return "", fmt.Errorf("%s is not running with a serial socket", name)
	}
	return st.SerialPath, nil
}

// AttachISO sets the ISO (and swaps the medium live if the VM runs).
func (m *Manager) AttachISO(name, iso string) error {
	iso = expandHome(iso)
	dir, err := m.dir(name)
	if err != nil {
		return err
	}
	path, err := m.resolveISO(iso, dir)
	if err != nil {
		return err
	}
	if err := m.Update(name, func(c *Config) error { c.ISO = iso; return nil }); err != nil {
		return err
	}
	if m.Status(name) != Stopped {
		err := m.withQMP(name, func(c *qemu.Client) error {
			_, err := c.Execute("blockdev-change-medium",
				map[string]any{"id": "cd0", "filename": path, "format": "raw"})
			return err
		})
		if err != nil {
			return fmt.Errorf("saved, but live media change failed: %w", err)
		}
	}
	m.event(name, "ISO attached: "+filepath.Base(path))
	return nil
}

// EjectISO clears the ISO.
func (m *Manager) EjectISO(name string) error {
	if err := m.Update(name, func(c *Config) error { c.ISO = ""; return nil }); err != nil {
		return err
	}
	if m.Status(name) != Stopped {
		err := m.withQMP(name, func(c *qemu.Client) error {
			_, err := c.Execute("eject", map[string]any{"id": "cd0", "force": true})
			return err
		})
		if err != nil {
			return fmt.Errorf("saved, but live eject failed: %w", err)
		}
	}
	m.event(name, "ISO ejected")
	return nil
}

// SetBoot selects the boot device (applies on next start).
func (m *Manager) SetBoot(name, dev string) error {
	return m.Update(name, func(c *Config) error { c.Boot = dev; return nil })
}

func parseUSB(id string) (qemu.USBDevice, uint64, uint64, error) {
	if !usbRE.MatchString(id) {
		return qemu.USBDevice{}, 0, 0, fmt.Errorf("invalid USB id %q (expected vvvv:pppp, see lsusb)", id)
	}
	v, p, _ := strings.Cut(id, ":")
	vi, _ := strconv.ParseUint(v, 16, 16)
	pi, _ := strconv.ParseUint(p, 16, 16)
	return qemu.USBDevice{VendorID: v, ProductID: p}, vi, pi, nil
}

// USBAttach adds a host USB device (live when running, and persistently).
func (m *Manager) USBAttach(name, id string) error {
	dev, vi, pi, err := parseUSB(id)
	if err != nil {
		return err
	}
	id = strings.ToLower(id)
	if m.Status(name) != Stopped {
		err := m.withQMP(name, func(c *qemu.Client) error {
			_, err := c.Execute("device_add", map[string]any{
				"driver": "usb-host", "vendorid": vi, "productid": pi, "id": dev.ID()})
			return err
		})
		if err != nil {
			return err
		}
	}
	return m.Update(name, func(c *Config) error {
		for _, u := range c.USB {
			if strings.EqualFold(u, id) {
				return nil
			}
		}
		c.USB = append(c.USB, id)
		return nil
	})
}

// USBRemove detaches a USB device.
func (m *Manager) USBRemove(name, id string) error {
	dev, _, _, err := parseUSB(id)
	if err != nil {
		return err
	}
	if m.Status(name) != Stopped {
		err := m.withQMP(name, func(c *qemu.Client) error {
			_, err := c.Execute("device_del", map[string]any{"id": dev.ID()})
			return err
		})
		if err != nil {
			return err
		}
	}
	return m.Update(name, func(c *Config) error {
		var keep []string
		for _, u := range c.USB {
			if !strings.EqualFold(u, id) {
				keep = append(keep, u)
			}
		}
		c.USB = keep
		return nil
	})
}

// ---- disks and snapshots ----

func (m *Manager) disksDir(name string) (string, error) {
	dir, err := m.dir(name)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "disks"), nil
}

func (m *Manager) mainDisk(name string) (string, error) {
	cfg, err := m.Load(name)
	if err != nil {
		return "", err
	}
	dd, err := m.disksDir(name)
	if err != nil {
		return "", err
	}
	return SafeJoin(dd, cfg.Disk)
}

// DiskInfo returns info for the main disk and attached extra disks.
func (m *Manager) DiskInfo(name string) ([]storage.Disk, error) {
	cfg, err := m.Load(name)
	if err != nil {
		return nil, err
	}
	dd, _ := m.disksDir(name)
	var out []storage.Disk
	for _, f := range append([]string{cfg.Disk}, cfg.ExtraDisks...) {
		p, err := SafeJoin(dd, f)
		if err != nil {
			return nil, err
		}
		d, err := storage.Info(p)
		if err != nil {
			return nil, err
		}
		d.Filename = f
		out = append(out, d)
	}
	return out, nil
}

// ResizeDisk grows the main disk of a stopped VM.
func (m *Manager) ResizeDisk(name, size string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	p, err := m.mainDisk(name)
	if err != nil {
		return err
	}
	if err := storage.Resize(p, size); err != nil {
		return err
	}
	m.event(name, "Disk resized: "+size)
	return nil
}

// AddDisk creates a new disk file and attaches it.
func (m *Manager) AddDisk(name, file, size, format string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	if err := ValidateBaseName(file); err != nil {
		return err
	}
	dd, err := m.disksDir(name)
	if err != nil {
		return err
	}
	if err := storage.CreateDisk(filepath.Join(dd, file), format, size); err != nil {
		return err
	}
	return m.Update(name, func(c *Config) error { c.ExtraDisks = append(c.ExtraDisks, file); return nil })
}

// AttachDisk attaches an existing file from the VM's disks/ directory.
func (m *Manager) AttachDisk(name, file string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	dd, err := m.disksDir(name)
	if err != nil {
		return err
	}
	p, err := SafeJoin(dd, file)
	if err != nil {
		return err
	}
	if _, err := os.Stat(p); err != nil {
		return fmt.Errorf("%s not found in disks/", file)
	}
	return m.Update(name, func(c *Config) error {
		for _, e := range append([]string{c.Disk}, c.ExtraDisks...) {
			if e == file {
				return fmt.Errorf("%s is already attached", file)
			}
		}
		c.ExtraDisks = append(c.ExtraDisks, file)
		return nil
	})
}

// DetachDisk detaches an extra disk (the file is kept).
func (m *Manager) DetachDisk(name, file string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	return m.Update(name, func(c *Config) error {
		var keep []string
		found := false
		for _, e := range c.ExtraDisks {
			if e == file {
				found = true
				continue
			}
			keep = append(keep, e)
		}
		if !found {
			return fmt.Errorf("%s is not an attached extra disk", file)
		}
		c.ExtraDisks = keep
		return nil
	})
}

// Snapshots lists internal snapshots of the main disk.
func (m *Manager) Snapshots(name string) ([]storage.Snapshot, error) {
	p, err := m.mainDisk(name)
	if err != nil {
		return nil, err
	}
	return storage.ListSnapshots(p)
}

// CreateSnapshot snapshots the main disk of a stopped VM.
func (m *Manager) CreateSnapshot(name, snap string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	p, err := m.mainDisk(name)
	if err != nil {
		return err
	}
	if err := storage.CreateSnapshot(p, snap); err != nil {
		return err
	}
	m.event(name, "Snapshot created: "+snap)
	return nil
}

// RestoreSnapshot reverts the main disk of a stopped VM.
func (m *Manager) RestoreSnapshot(name, snap string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	p, err := m.mainDisk(name)
	if err != nil {
		return err
	}
	if err := storage.RestoreSnapshot(p, snap); err != nil {
		return err
	}
	m.event(name, "Snapshot restored: "+snap)
	return nil
}

// DeleteSnapshot removes a snapshot of a stopped VM.
func (m *Manager) DeleteSnapshot(name, snap string) error {
	if err := m.requireStopped(name); err != nil {
		return err
	}
	p, err := m.mainDisk(name)
	if err != nil {
		return err
	}
	return storage.DeleteSnapshot(p, snap)
}
