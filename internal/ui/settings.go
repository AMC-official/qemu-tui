package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AMC-official/qemu-tui/internal/config"
	"github.com/AMC-official/qemu-tui/internal/vm"
)

type setKind int

const (
	kindChoice setKind = iota // left/right cycles through choices
	kindText                  // enter edits the value
	kindToggle                // on/off
	kindAction                // enter runs something
	kindInfo                  // read-only
)

type setItem struct {
	label   string
	kind    setKind
	choices []string
	hint    string
	get     func() string
	set     func(string) error
	run     func() tea.Cmd
}

type settingsState struct {
	title   string
	global  bool
	vmName  string
	cfg     vm.Config
	items   []setItem
	cursor  int
	editing bool
	input   textinput.Model
}

// move jumps to the next editable row in direction d.
func (st *settingsState) move(d int) {
	for i := st.cursor + d; i >= 0 && i < len(st.items); i += d {
		if st.items[i].kind != kindInfo {
			st.cursor = i
			return
		}
	}
}

func newSettings(title string) *settingsState {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 256
	return &settingsState{title: title, input: in}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func expandPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// ---- opening ----

func (m *Model) openVMSettings(name string) {
	c, err := m.mgr.Load(name)
	if err != nil {
		m.setStatus(err.Error(), true)
		m.scr = scList
		return
	}
	st := newSettings("Settings: " + name)
	st.vmName, st.cfg = name, c
	st.items = m.vmItems(st)
	st.move(-1)
	st.cursor = -1
	st.move(1)
	m.set, m.scr = st, scSettings
}

func (m *Model) openGlobalSettings() {
	st := newSettings("Settings: qemu-tui")
	st.global = true
	st.items = m.globalItems()
	st.cursor = -1
	st.move(1)
	m.set, m.scr = st, scSettings
}

func (m *Model) reloadSettings() {
	if m.set != nil && !m.set.global {
		if c, err := m.mgr.Load(m.set.vmName); err == nil {
			m.set.cfg = c
		}
	}
}

func (m *Model) reloadGlobal() {
	c, err := config.Load()
	if err != nil {
		m.setStatus("config problem: "+err.Error(), true)
		return
	}
	m.cfg = c
	m.th = NewTheme(c.Theme, c.Colors)
	m.mgr.DefaultDisplay = c.DefaultDisplay
	m.mgr.ISODir = c.ISODir
	m.setStatus("config reloaded (VM/log folder changes need a restart)", false)
}

// ---- item lists ----

func (m *Model) vmItems(st *settingsState) []setItem {
	name := st.vmName
	reload := func() {
		if c, err := m.mgr.Load(name); err == nil {
			st.cfg = c
		}
	}
	upd := func(f func(*vm.Config)) error {
		err := m.mgr.Update(name, func(c *vm.Config) error { f(c); return nil })
		reload()
		return err
	}
	number := func(v string, lo int) (int, error) {
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < lo {
			return 0, fmt.Errorf("enter a whole number (at least %d)", lo)
		}
		return n, nil
	}
	choice := func(label, hint string, choices []string, get func() string, f func(*vm.Config, string)) setItem {
		return setItem{label: label, kind: kindChoice, choices: choices, hint: hint, get: get,
			set: func(v string) error { return upd(func(c *vm.Config) { f(c, v) }) }}
	}
	return []setItem{
		{label: "Architecture", kind: kindInfo, get: func() string { return st.cfg.Architecture }},
		{label: "Disk", kind: kindInfo, get: func() string { return st.cfg.Disk + " (" + dash(st.cfg.DiskSize) + ")" }},
		{label: "Memory (MiB)", kind: kindText, hint: "RAM given to the guest.",
			get: func() string { return strconv.Itoa(st.cfg.Memory) },
			set: func(v string) error {
				n, err := number(v, 16)
				if err != nil {
					return err
				}
				return upd(func(c *vm.Config) { c.Memory = n })
			}},
		{label: "CPU cores", kind: kindText, hint: "Virtual CPU cores for the guest.",
			get: func() string { return strconv.Itoa(st.cfg.CPUs) },
			set: func(v string) error {
				n, err := number(v, 1)
				if err != nil {
					return err
				}
				return upd(func(c *vm.Config) { c.CPUs = n })
			}},
		choice("Display", "gtk/sdl open a window; vnc/spice listen on localhost only.", vm.Displays,
			func() string { return st.cfg.Display }, func(c *vm.Config, v string) { c.Display = v }),
		choice("Boot device", "Tried first; the others are fallbacks (x86 guests).", vm.BootDevices,
			func() string { return st.cfg.Boot }, func(c *vm.Config, v string) { c.Boot = v }),
		choice("Firmware", "UEFI needs OVMF/edk2 installed. ARM and RISC-V guests always use UEFI.", vm.Firmwares,
			func() string { return st.cfg.Firmware }, func(c *vm.Config, v string) { c.Firmware = v }),
		choice("Network", "user = simple NAT, tap = host tap interface, none = offline.", vm.NetModes,
			func() string { return st.cfg.NetworkMode },
			func(c *vm.Config, v string) {
				c.NetworkMode = v
				if v == "tap" && c.TapIface == "" {
					c.TapIface = "tap0"
				}
			}),
		{label: "TAP interface", kind: kindText, hint: "Only used when Network is tap.",
			get: func() string { return st.cfg.TapIface },
			set: func(v string) error { return upd(func(c *vm.Config) { c.TapIface = v }) }},
		choice("Disk bus", "Use ide for old guests (Windows XP, DOS) that have no virtio drivers.", []string{"virtio", "ide"},
			func() string { return st.cfg.DiskBus }, func(c *vm.Config, v string) { c.DiskBus = v }),
		{label: "Serial console", kind: kindToggle, choices: []string{"off", "on"},
			hint: "Adds a serial port reachable with :serial in the VM console.",
			get: func() string {
				if st.cfg.Serial {
					return "on"
				}
				return "off"
			},
			set: func(v string) error { return upd(func(c *vm.Config) { c.Serial = v == "on" }) }},
		{label: "MAC address", kind: kindText, hint: "Format 52:54:00:12:34:56.",
			get: func() string { return st.cfg.MAC },
			set: func(v string) error { return upd(func(c *vm.Config) { c.MAC = v }) }},
		{label: "ISO", kind: kindText, hint: "Name in the ISO folder or a full path. Empty = eject. Works while running.",
			get: func() string { return st.cfg.ISO },
			set: func(v string) error {
				var err error
				if v == "" || strings.EqualFold(v, "none") {
					err = m.mgr.EjectISO(name)
				} else {
					err = m.mgr.AttachISO(name, v)
				}
				reload()
				return err
			}},
		{label: "Edit raw config file...", kind: kindAction, hint: "Advanced: opens config.json in your editor.",
			run: func() tea.Cmd {
				return m.editFile(filepath.Join(m.mgr.Root, name, "config.json"), name, false)
			}},
	}
}

func (m *Model) globalItems() []setItem {
	save := func(f func(c *config.Config)) error {
		f(&m.cfg)
		m.th = NewTheme(m.cfg.Theme, m.cfg.Colors)
		m.mgr.DefaultDisplay = m.cfg.DefaultDisplay
		m.mgr.ISODir = m.cfg.ISODir
		return m.cfg.Save()
	}
	return []setItem{
		{label: "Default display", kind: kindChoice, choices: vm.Displays,
			hint: "Used for newly created VMs.",
			get:  func() string { return m.cfg.DefaultDisplay },
			set:  func(v string) error { return save(func(c *config.Config) { c.DefaultDisplay = v }) }},
		{label: "Theme", kind: kindChoice, choices: []string{"dark", "light"},
			get: func() string { return m.cfg.Theme },
			set: func(v string) error { return save(func(c *config.Config) { c.Theme = v }) }},
		{label: "ISO folder", kind: kindText, hint: "Where Attach Media looks for .iso files.",
			get: func() string { return m.cfg.ISODir },
			set: func(v string) error {
				if v == "" {
					return fmt.Errorf("the ISO folder cannot be empty")
				}
				p := expandPath(v)
				os.MkdirAll(p, 0o755)
				return save(func(c *config.Config) { c.ISODir = p })
			}},
		{label: "Raw file editor", kind: kindText, hint: "Command used by \"Edit raw config file\" (for example nano or micro).",
			get: func() string { return m.cfg.Editor },
			set: func(v string) error {
				if v == "" {
					return fmt.Errorf("enter an editor command")
				}
				return save(func(c *config.Config) { c.Editor = v })
			}},
		{label: "VM folder", kind: kindInfo, get: func() string { return m.cfg.VMDir }},
		{label: "Log folder", kind: kindInfo, get: func() string { return m.cfg.LogDir }},
		{label: "Config file", kind: kindInfo, get: func() string { return config.Path() }},
		{label: "Edit raw config file...", kind: kindAction,
			hint: "Advanced: key bindings, custom colors, folders.",
			run: func() tea.Cmd {
				if err := config.EnsureFile(m.cfg); err != nil {
					m.setStatus(err.Error(), true)
					return nil
				}
				return m.editFile(config.Path(), "", true)
			}},
	}
}

// ---- input ----

func (m *Model) vmIsRunning() bool {
	if m.set == nil || m.set.global {
		return false
	}
	for _, in := range m.infos {
		if in.Config.Name == m.set.vmName {
			return in.State != vm.Stopped
		}
	}
	return false
}

func (m *Model) applySetting(it setItem, v string) {
	if it.set == nil {
		return
	}
	if err := it.set(v); err != nil {
		m.setStatus(err.Error(), true)
		return
	}
	if m.vmIsRunning() && it.label != "ISO" {
		m.setStatus("saved - applies on next start", false)
		return
	}
	m.setStatus("saved", false)
}

func (m *Model) cycle(it setItem, d int) {
	if (it.kind != kindChoice && it.kind != kindToggle) || len(it.choices) == 0 || it.get == nil {
		return
	}
	cur := it.get()
	idx := 0
	for i, c := range it.choices {
		if c == cur {
			idx = i
		}
	}
	n := len(it.choices)
	m.applySetting(it, it.choices[(idx+d+n)%n])
}

func (m *Model) keySettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.set
	it := st.items[st.cursor]
	s := k.String()
	if st.editing {
		switch s {
		case "esc":
			st.editing = false
			st.input.Blur()
		case "enter":
			v := strings.TrimSpace(st.input.Value())
			st.editing = false
			st.input.Blur()
			m.applySetting(it, v)
		default:
			var cmd tea.Cmd
			st.input, cmd = st.input.Update(k)
			return m, cmd
		}
		return m, nil
	}
	switch s {
	case "esc", "q":
		m.scr = scMenu
		if st.global {
			m.scr = scList
		}
		return m, m.loadCmd()
	case "up", "k":
		st.move(-1)
	case "down", "j":
		st.move(1)
	case "left", "h":
		m.cycle(it, -1)
	case "right", "l", " ":
		m.cycle(it, 1)
	case "enter":
		switch it.kind {
		case kindText:
			st.editing = true
			st.input.SetValue(it.get())
			st.input.CursorEnd()
			st.input.Focus()
		case kindChoice, kindToggle:
			m.cycle(it, 1)
		case kindAction:
			if it.run != nil {
				return m, it.run()
			}
		}
	}
	return m, nil
}

func (m *Model) viewSettings() string {
	st := m.set
	var b strings.Builder
	b.WriteString(m.header(st.title))
	for i, it := range st.items {
		sel := i == st.cursor
		val := ""
		if it.get != nil {
			val = it.get()
		}
		switch it.kind {
		case kindChoice:
			if sel {
				val = "◀ " + val + " ▶"
			}
		case kindToggle:
			box := "[ ] "
			if val == "on" {
				box = "[x] "
			}
			val = box + val
		case kindText:
			if val == "" {
				val = "(none)"
			}
		}
		mark := "   "
		label := fmt.Sprintf("%-18s", trunc(it.label, 18))
		if it.kind == kindAction {
			label = it.label
		}
		switch {
		case sel && st.editing && it.kind == kindText:
			mark = m.bold(m.th.Accent, " ▸ ")
			val = st.input.View()
		case sel:
			mark = m.bold(m.th.Accent, " ▸ ")
			label = m.bold(m.th.Accent, label)
			val = m.bold(m.th.Text, val)
		case it.kind == kindInfo:
			val = m.paint(m.th.Dim, val)
		}
		b.WriteString(m.clip(mark+label+" "+val) + "\n")
	}
	if h := st.items[st.cursor].hint; h != "" {
		b.WriteString("\n")
		for _, l := range wrapText(h, max(m.w-4, 10)) {
			b.WriteString(m.paint(m.th.Dim, "   "+l) + "\n")
		}
	}
	hints := "↑↓ move · ←/→ change · enter edit · esc back"
	if st.editing {
		hints = "type a value · enter save · esc cancel"
	} else if m.vmIsRunning() {
		hints = "VM is running: changes apply on next start · " + hints
	}
	b.WriteString("\n" + m.footer(hints))
	return b.String()
}
