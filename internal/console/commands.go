// Package console implements the global and per-VM command console.
package console

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AMC-official/qemu-tui/internal/storage"
	"github.com/AMC-official/qemu-tui/internal/system"
	"github.com/AMC-official/qemu-tui/internal/vm"
)

// ErrExit is returned by :exit.
var ErrExit = errors.New("exit")

// Console executes console commands. Scope is "" for the global console or a
// VM name for a per-VM console (then the <vm> argument is implied).
type Console struct {
	Mgr     *vm.Manager
	Scope   string
	Sampler *system.Sampler
}

type command struct {
	usage string
	help  string
	run   func(c *Console, args []string) (string, error)
}

var commands map[string]command

func init() {
	commands = map[string]command{
		"help":      {"help", "show this help", cmdHelp},
		"list":      {"list [snapshots]", "list VMs (or snapshots of a VM)", cmdList},
		"start":     {"start <vm>", "start a VM", simple((*vm.Manager).Start, "started")},
		"stop":      {"stop <vm>", "ACPI power-down (graceful)", simple((*vm.Manager).Stop, "power-down requested")},
		"restart":   {"restart <vm>", "reset the guest", simple((*vm.Manager).Restart, "reset")},
		"kill":      {"kill <vm>", "force stop (quit QEMU)", simple((*vm.Manager).ForceStop, "force stopped")},
		"pause":     {"pause <vm>", "pause the guest", simple((*vm.Manager).Pause, "paused")},
		"resume":    {"resume <vm>", "resume the guest", simple((*vm.Manager).Resume, "resumed")},
		"status":    {"status [vm]", "overview, or details of one VM", cmdStatus},
		"top":       {"top", "live resource view", cmdTop},
		"create":    {"create <name> [key=value ...]", "create a VM (keys: mem cpus arch disk fmt fw net tap display iso boot)", cmdCreate},
		"delete":    {"delete <vm>", "delete a stopped VM and its files", simple((*vm.Manager).Delete, "deleted")},
		"clone":     {"clone <vm> <new>", "clone a stopped VM", cmdClone},
		"snapshot":  {"snapshot <vm> <name>", "create a snapshot (VM stopped)", cmdSnapshot},
		"restore":   {"restore <vm> <name>", "restore a snapshot (VM stopped)", cmdRestore},
		"snapshots": {"snapshots <vm>", "list snapshots", cmdSnapshots},
		"logs":      {"logs [vm]", "show recent log lines", cmdLogs},
		"usb":       {"usb attach|remove <vm> <vvvv:pppp>", "attach/remove a host USB device", cmdUSB},
		"disk":      {"disk info|resize|create|attach|detach <vm> ...", "disk management (qemu-img)", cmdDisk},
		"iso":       {"iso list|attach|eject <vm> [file]", "ISO management", cmdISO},
		"boot":      {"boot <vm> disk|cdrom|network", "select boot device (next start)", cmdBoot},
		"monitor":   {"monitor <vm> <cmd>", "run a QEMU monitor command, e.g. info status", cmdMonitor},
		"serial":    {"serial [vm]", "how to reach the serial console", cmdSerial},
		"exit":      {"exit", "leave the console", func(*Console, []string) (string, error) { return "", ErrExit }},
	}
	commands["quit"] = commands["exit"]
}

// Tokenize splits a line into arguments. It understands single and double
// quotes and backslash escapes, and never invokes a shell.
func Tokenize(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	in, quote, esc := false, rune(0), false
	for _, r := range line {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && quote != '\'':
			esc, in = true, true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, in = r, true
		case r == ' ' || r == '\t':
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
		default:
			cur.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if in {
		out = append(out, cur.String())
	}
	return out, nil
}

// Execute runs one command line and returns its output.
func (c *Console) Execute(line string) (string, error) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), ":"))
	if line == "" {
		return "", nil
	}
	args, err := Tokenize(line)
	if err != nil {
		return "", err
	}
	return c.ExecuteArgs(args)
}

// ExecuteArgs runs an already split command (used for CLI arguments).
func (c *Console) ExecuteArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	cmd, ok := commands[strings.ToLower(strings.TrimPrefix(args[0], ":"))]
	if !ok {
		return "", fmt.Errorf("unknown command %q (try :help)", args[0])
	}
	return cmd.run(c, args[1:])
}

// target resolves the VM argument: implied in a per-VM console.
func (c *Console) target(args []string, usage string) (string, []string, error) {
	if c.Scope != "" {
		if len(args) > 0 && args[0] == c.Scope {
			args = args[1:]
		}
		return c.Scope, args, nil
	}
	if len(args) == 0 {
		return "", nil, fmt.Errorf("usage: :%s", usage)
	}
	return args[0], args[1:], nil
}

func simple(f func(*vm.Manager, string) error, done string) func(*Console, []string) (string, error) {
	return func(c *Console, args []string) (string, error) {
		name, _, err := c.target(args, "<command> <vm>")
		if err != nil {
			return "", err
		}
		if err := f(c.Mgr, name); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s", name, done), nil
	}
}

func cmdHelp(c *Console, _ []string) (string, error) {
	names := make([]string, 0, len(commands))
	for n := range commands {
		if n != "quit" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "  :%-44s %s\n", commands[n].usage, commands[n].help)
	}
	if c.Scope != "" {
		fmt.Fprintf(&b, "\nIn the %s console the <vm> argument is implied.", c.Scope)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func cmdList(c *Console, args []string) (string, error) {
	if len(args) > 0 && args[0] == "snapshots" {
		return cmdSnapshots(c, args[1:])
	}
	infos, err := c.Mgr.List()
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "No VMs yet. Create one with :create <name>", nil
	}
	var b strings.Builder
	for _, in := range infos {
		fmt.Fprintf(&b, "%-24s %-8s %d CPU  %d MB  %s\n", in.Config.Name, in.State,
			in.Config.CPUs, in.Config.Memory, in.Config.Architecture)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func cmdStatus(c *Console, args []string) (string, error) {
	if c.Scope == "" && len(args) == 0 {
		return cmdList(c, nil)
	}
	name, _, err := c.target(args, "status [vm]")
	if err != nil {
		return "", err
	}
	in, err := c.Mgr.Detail(name)
	if err != nil {
		return "", err
	}
	cf := in.Config
	var b strings.Builder
	fmt.Fprintf(&b, "VM:        %s (%s)\nState:     %s\n", cf.Name, cf.Architecture, in.State)
	fmt.Fprintf(&b, "Resources: %d CPU, %d MB RAM, disk %s (%s)\n", cf.CPUs, cf.Memory, cf.DiskSize, cf.DiskFormat)
	fmt.Fprintf(&b, "Network:   %s  MAC %s\n", cf.NetworkMode, cf.MAC)
	fmt.Fprintf(&b, "Display:   %s   Firmware: %s   Boot: %s\n", cf.Display, cf.Firmware, cf.Boot)
	if cf.ISO != "" {
		fmt.Fprintf(&b, "ISO:       %s\n", cf.ISO)
	}
	if in.PID != 0 {
		fmt.Fprintf(&b, "PID:       %d\nRuntime:   %s\n", in.PID, system.HumanDuration(in.Uptime))
		if st, err := c.sampler().Sample(in.PID); err == nil {
			fmt.Fprintf(&b, "CPU:       %.0f%%\nMemory:    %s\nDisk I/O:  %s read, %s written (total)\n",
				st.CPUPercent, system.HumanBytes(st.RSSBytes),
				system.HumanBytes(st.ReadBytes), system.HumanBytes(st.WriteBytes))
		}
		if out, err := c.Mgr.Monitor(name, "info network"); err == nil {
			fmt.Fprintf(&b, "Net links:\n%s\n", indent(strings.TrimSpace(out)))
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func indent(s string) string { return "  " + strings.ReplaceAll(s, "\n", "\n  ") }

func (c *Console) sampler() *system.Sampler {
	if c.Sampler == nil {
		c.Sampler = system.NewSampler()
	}
	return c.Sampler
}

func bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	n := int(pct / 100 * float64(width))
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

func cmdTop(c *Console, _ []string) (string, error) {
	infos, _ := c.Mgr.List()
	var b strings.Builder
	any := false
	for _, in := range infos {
		if in.PID == 0 {
			continue
		}
		any = true
		st, err := c.sampler().Sample(in.PID)
		if err != nil {
			continue
		}
		cpu := st.CPUPercent / float64(in.Config.CPUs)
		ram := float64(st.RSSBytes) / (float64(in.Config.Memory) * 1024 * 1024) * 100
		fmt.Fprintf(&b, "%s (%s)\n  CPU: %s %3.0f%%\n  RAM: %s %3.0f%%  %s\n", in.Config.Name, in.State,
			bar(cpu, 20), cpu, bar(ram, 20), ram, system.HumanBytes(st.RSSBytes))
	}
	if !any {
		return "No running VMs.", nil
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func atoi(key, v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number, got %q", key, v)
	}
	return n, nil
}

func cmdCreate(c *Console, args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("usage: :create <name> [mem=2048 cpus=2 arch=x86_64 disk=20G fmt=qcow2 fw=bios net=user display=gtk iso=file.iso]")
	}
	cfg := vm.Config{Name: args[0], NetworkMode: "user"}
	for _, kv := range args[1:] {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return "", fmt.Errorf("expected key=value, got %q", kv)
		}
		var err error
		switch strings.ToLower(k) {
		case "mem", "memory", "ram":
			cfg.Memory, err = atoi(k, v)
		case "cpus", "cpu":
			cfg.CPUs, err = atoi(k, v)
		case "arch":
			cfg.Architecture = v
		case "disk":
			cfg.DiskSize = v
		case "fmt", "format":
			cfg.DiskFormat = v
		case "fw", "firmware":
			cfg.Firmware = v
		case "net", "network":
			cfg.NetworkMode = v
		case "tap":
			cfg.TapIface = v
			cfg.NetworkMode = "tap"
		case "display":
			cfg.Display = v
		case "iso":
			cfg.ISO = v
		case "boot":
			cfg.Boot = v
		default:
			err = fmt.Errorf("unknown option %q", k)
		}
		if err != nil {
			return "", err
		}
	}
	if err := c.Mgr.Create(cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Created %s. Start it with :start %s", cfg.Name, cfg.Name), nil
}

func cmdClone(c *Console, args []string) (string, error) {
	name, rest, err := c.target(args, "clone <vm> <new>")
	if err != nil {
		return "", err
	}
	if len(rest) != 1 {
		return "", errors.New("usage: :clone <vm> <new-name>")
	}
	if err := c.Mgr.Clone(name, rest[0]); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cloned %s -> %s", name, rest[0]), nil
}

func snapArgs(c *Console, args []string, usage string) (string, string, error) {
	name, rest, err := c.target(args, usage)
	if err != nil {
		return "", "", err
	}
	if len(rest) != 1 {
		return "", "", fmt.Errorf("usage: :%s", usage)
	}
	return name, rest[0], nil
}

func cmdSnapshot(c *Console, args []string) (string, error) {
	name, snap, err := snapArgs(c, args, "snapshot <vm> <name>")
	if err != nil {
		return "", err
	}
	if err := c.Mgr.CreateSnapshot(name, snap); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: snapshot %q created", name, snap), nil
}

func cmdRestore(c *Console, args []string) (string, error) {
	name, snap, err := snapArgs(c, args, "restore <vm> <name>")
	if err != nil {
		return "", err
	}
	if err := c.Mgr.RestoreSnapshot(name, snap); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: restored snapshot %q", name, snap), nil
}

// FormatSnapshots renders snapshots as in the spec.
func FormatSnapshots(snaps []storage.Snapshot) string {
	if len(snaps) == 0 {
		return "No snapshots."
	}
	var b strings.Builder
	for _, s := range snaps {
		fmt.Fprintf(&b, "Snapshot:\n%s\n\nCreated:\n%s\n\n", s.Name, s.Created().Format("2006-01-02"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdSnapshots(c *Console, args []string) (string, error) {
	name, _, err := c.target(args, "snapshots <vm>")
	if err != nil {
		return "", err
	}
	s, err := c.Mgr.Snapshots(name)
	if err != nil {
		return "", err
	}
	return FormatSnapshots(s), nil
}

func cmdLogs(c *Console, args []string) (string, error) {
	var lines []string
	var err error
	if c.Scope == "" && len(args) == 0 {
		lines, err = c.Mgr.GlobalLog(40)
	} else {
		var name string
		if name, _, err = c.target(args, "logs [vm]"); err == nil {
			lines, err = c.Mgr.Logs(name, 40)
		}
	}
	if err != nil {
		return "", err
	}
	if len(lines) == 0 {
		return "(no log entries)", nil
	}
	return strings.Join(lines, "\n"), nil
}

func cmdUSB(c *Console, args []string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("usage: :usb attach|remove <vm> <vvvv:pppp>   (ids from lsusb)")
	}
	action := strings.ToLower(args[0])
	name, rest, err := c.target(args[1:], "usb attach|remove <vm> <vvvv:pppp>")
	if err != nil {
		return "", err
	}
	if len(rest) != 1 {
		return "", errors.New("usage: :usb attach|remove <vm> <vvvv:pppp>")
	}
	switch action {
	case "attach":
		err = c.Mgr.USBAttach(name, rest[0])
	case "remove", "detach":
		err = c.Mgr.USBRemove(name, rest[0])
	default:
		err = fmt.Errorf("unknown usb action %q", action)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: usb %s %s", name, action, rest[0]), nil
}

func cmdDisk(c *Console, args []string) (string, error) {
	const usage = "disk info|resize|create|attach|detach <vm> ..."
	if len(args) == 0 {
		return "", fmt.Errorf("usage: :%s", usage)
	}
	action := strings.ToLower(args[0])
	name, rest, err := c.target(args[1:], usage)
	if err != nil {
		return "", err
	}
	switch action {
	case "info":
		disks, err := c.Mgr.DiskInfo(name)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, d := range disks {
			fmt.Fprintf(&b, "%s  %s  virtual %s  on disk %s  snapshots %d\n", d.Filename, d.Format,
				system.HumanBytes(uint64(d.VirtualSize)), system.HumanBytes(uint64(d.ActualSize)), len(d.Snapshots))
		}
		return strings.TrimRight(b.String(), "\n"), nil
	case "resize":
		if len(rest) != 1 {
			return "", errors.New("usage: :disk resize <vm> <size|+size>")
		}
		return "Disk resized (grow the partition inside the guest too)", c.Mgr.ResizeDisk(name, rest[0])
	case "create":
		if len(rest) < 2 {
			return "", errors.New("usage: :disk create <vm> <file> <size> [qcow2|raw]")
		}
		format := "qcow2"
		if len(rest) > 2 {
			format = rest[2]
		}
		return "Disk created and attached", c.Mgr.AddDisk(name, rest[0], rest[1], format)
	case "attach":
		if len(rest) != 1 {
			return "", errors.New("usage: :disk attach <vm> <file in disks/>")
		}
		return "Disk attached", c.Mgr.AttachDisk(name, rest[0])
	case "detach":
		if len(rest) != 1 {
			return "", errors.New("usage: :disk detach <vm> <file>")
		}
		return "Disk detached", c.Mgr.DetachDisk(name, rest[0])
	}
	return "", fmt.Errorf("unknown disk action %q", action)
}

func cmdISO(c *Console, args []string) (string, error) {
	const usage = "iso list|attach|eject <vm> [file]"
	if len(args) == 0 {
		return "", fmt.Errorf("usage: :%s", usage)
	}
	action := strings.ToLower(args[0])
	if action == "list" {
		isos := c.Mgr.ListISOs()
		if len(isos) == 0 {
			return "No ISOs in " + c.Mgr.ISODir, nil
		}
		return strings.Join(isos, "\n"), nil
	}
	name, rest, err := c.target(args[1:], usage)
	if err != nil {
		return "", err
	}
	switch action {
	case "attach":
		if len(rest) != 1 {
			return "", errors.New("usage: :iso attach <vm> <file>")
		}
		return "ISO attached", c.Mgr.AttachISO(name, rest[0])
	case "eject":
		return "ISO ejected", c.Mgr.EjectISO(name)
	}
	return "", fmt.Errorf("unknown iso action %q", action)
}

func cmdBoot(c *Console, args []string) (string, error) {
	name, rest, err := c.target(args, "boot <vm> disk|cdrom|network")
	if err != nil {
		return "", err
	}
	if len(rest) != 1 {
		return "", errors.New("usage: :boot <vm> disk|cdrom|network")
	}
	if err := c.Mgr.SetBoot(name, rest[0]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: boot from %s (applies on next start)", name, rest[0]), nil
}

func cmdMonitor(c *Console, args []string) (string, error) {
	name, rest, err := c.target(args, "monitor <vm> <command>")
	if err != nil {
		return "", err
	}
	if len(rest) == 0 {
		return "", errors.New("usage: :monitor <vm> <command>   e.g. info status, info block, info network")
	}
	out, err := c.Mgr.Monitor(name, strings.Join(rest, " "))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "(ok)", nil
	}
	return strings.TrimRight(out, "\r\n "), nil
}

func cmdSerial(c *Console, args []string) (string, error) {
	name, _, err := c.target(args, "serial [vm]")
	if err != nil {
		return "", err
	}
	p, err := c.Mgr.SerialPath(name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Serial console socket:\n  %s\nConnect with:\n  socat -,raw,echo=0,escape=0x1d UNIX-CONNECT:%s", p, p), nil
}

// ---- completion ----

var vmFirst = map[string]bool{"start": true, "stop": true, "restart": true, "kill": true, "pause": true,
	"resume": true, "delete": true, "clone": true, "snapshot": true, "restore": true, "snapshots": true,
	"status": true, "logs": true, "boot": true, "monitor": true, "serial": true}

var subcommands = map[string][]string{
	"usb":  {"attach", "remove"},
	"disk": {"info", "resize", "create", "attach", "detach"},
	"iso":  {"list", "attach", "eject"},
	"list": {"snapshots"},
	"boot": {"disk", "cdrom", "network"},
}

// Complete returns completion candidates for the last word of line.
func (c *Console) Complete(line string) []string {
	line = strings.TrimPrefix(line, ":")
	trailing := strings.HasSuffix(line, " ")
	fields := strings.Fields(line)
	if len(fields) == 0 || (len(fields) == 1 && !trailing) {
		prefix := ""
		if len(fields) == 1 {
			prefix = strings.ToLower(fields[0])
		}
		var out []string
		for n := range commands {
			if strings.HasPrefix(n, prefix) && n != "quit" {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out
	}
	cmd := strings.ToLower(fields[0])
	word, idx := "", len(fields)-1
	if trailing {
		idx = len(fields)
	} else {
		word = fields[len(fields)-1]
	}
	var pool []string
	switch {
	case c.Scope == "" && vmFirst[cmd] && idx == 1:
		pool = c.Mgr.Names()
	case subcommands[cmd] != nil && idx == 1:
		pool = subcommands[cmd]
	case c.Scope == "" && subcommands[cmd] != nil && idx == 2 && cmd != "list" && cmd != "boot":
		pool = c.Mgr.Names()
	case cmd == "boot" && ((c.Scope == "" && idx == 2) || (c.Scope != "" && idx == 1)):
		pool = subcommands["boot"]
	case cmd == "iso" && idx >= 2:
		pool = c.Mgr.ListISOs()
	}
	var out []string
	for _, p := range pool {
		if strings.HasPrefix(p, word) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// CommonPrefix returns the longest common prefix of the candidates.
func CommonPrefix(c []string) string {
	if len(c) == 0 {
		return ""
	}
	p := c[0]
	for _, s := range c[1:] {
		for !strings.HasPrefix(s, p) {
			p = p[:len(p)-1]
		}
	}
	return p
}

// ApplyCompletion replaces the last word of line with its completion.
func ApplyCompletion(line string, cands []string) string {
	if len(cands) == 0 {
		return line
	}
	p := CommonPrefix(cands)
	if strings.HasSuffix(line, " ") || line == "" {
		return line + p
	}
	i := strings.LastIndexAny(line, " :")
	res := line[:i+1] + p
	if len(cands) == 1 {
		res += " "
	}
	return res
}

// Banner is shown when a console opens.
func (c *Console) Banner() string {
	if c.Scope != "" {
		return fmt.Sprintf("%s console - :help for commands, esc to close", c.Scope)
	}
	return "qemu-tui console - :help for commands, esc to close   (" + time.Now().Format("15:04") + ")"
}
