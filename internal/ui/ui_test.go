package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/AMC-official/qemu-tui/internal/config"
	"github.com/AMC-official/qemu-tui/internal/vm"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	d := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(d, "cfg"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(d, "data"))
	mgr := vm.NewManager(filepath.Join(d, "vms"), filepath.Join(d, "logs"), filepath.Join(d, "isos"))
	dir := filepath.Join(mgr.Root, "a")
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := vm.Config{Name: "a"}
	c.ApplyDefaults()
	if err := vm.SaveConfig(filepath.Join(dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
	return New(mgr, config.Default())
}

func item(t *testing.T, st *settingsState, label string) setItem {
	t.Helper()
	for _, it := range st.items {
		if it.label == label {
			return it
		}
	}
	t.Fatalf("no setting %q", label)
	return setItem{}
}

func TestWrapText(t *testing.T) {
	got := wrapText("aaa bbb ccc ddd", 8)
	if len(got) != 2 || got[0] != "aaa bbb" || got[1] != "ccc ddd" {
		t.Fatalf("word wrap: %q", got)
	}
	got = wrapText("abcdefghijklmnopqrstuvwxyz", 10)
	if len(got) != 3 || got[0] != "abcdefghij" || got[2] != "uvwxyz" {
		t.Fatalf("hard wrap: %q", got)
	}
	if got := wrapText("short", 40); len(got) != 1 || got[0] != "short" {
		t.Fatalf("short: %q", got)
	}
	got = wrapText("  indented text that is long", 12)
	for i, l := range got {
		if len([]rune(l)) > 12 {
			t.Errorf("line %d too long: %q", i, l)
		}
		if i > 0 && !strings.HasPrefix(l, "  ") {
			t.Errorf("continuation %d lost indent: %q", i, l)
		}
	}
	// must always terminate, even with silly widths
	for w := -3; w < 12; w++ {
		wrapText(strings.Repeat("word ", 30), w)
		wrapText("        "+strings.Repeat("x", 50), w)
	}
}

func TestVMSettings(t *testing.T) {
	m := newTestModel(t)
	m.openVMSettings("a")
	st := m.set
	if m.scr != scSettings || st == nil {
		t.Fatal("settings screen not opened")
	}
	if st.items[st.cursor].kind == kindInfo {
		t.Error("cursor must start on an editable row")
	}

	m.applySetting(item(t, st, "Memory (MiB)"), "4096")
	if c, _ := m.mgr.Load("a"); c.Memory != 4096 {
		t.Fatalf("memory = %d", c.Memory)
	}
	m.applySetting(item(t, st, "Memory (MiB)"), "abc")
	if !m.statusErr {
		t.Error("bad number must show an error")
	}
	m.applySetting(item(t, st, "Network"), "tap")
	if c, _ := m.mgr.Load("a"); c.NetworkMode != "tap" || c.TapIface != "tap0" {
		t.Fatalf("tap: %+v", c)
	}
	m.cycle(item(t, st, "Serial console"), 1)
	if c, _ := m.mgr.Load("a"); !c.Serial {
		t.Error("serial toggle")
	}
	m.cycle(item(t, st, "Display"), 1)
	if c, _ := m.mgr.Load("a"); c.Display != "sdl" {
		t.Errorf("display cycle: %s", c.Display)
	}
	m.cycle(item(t, st, "Display"), -1)
	if c, _ := m.mgr.Load("a"); c.Display != "gtk" {
		t.Errorf("display cycle back: %s", c.Display)
	}
	m.applySetting(item(t, st, "Firmware"), "uefi")
	if got := item(t, st, "Firmware").get(); got != "uefi" {
		t.Errorf("firmware = %s", got)
	}
	m.applySetting(item(t, st, "MAC address"), "nonsense")
	if !m.statusErr {
		t.Error("invalid MAC must be rejected")
	}
	if c, _ := m.mgr.Load("a"); c.MAC == "nonsense" {
		t.Error("invalid MAC must not be saved")
	}
	m.applySetting(item(t, st, "ISO"), "missing.iso")
	if !m.statusErr {
		t.Error("missing ISO must be rejected")
	}
}

func TestGlobalSettings(t *testing.T) {
	m := newTestModel(t)
	m.openGlobalSettings()
	st := m.set
	m.applySetting(item(t, st, "Theme"), "light")
	if m.cfg.Theme != "light" || m.th.Accent != NewTheme("light", nil).Accent {
		t.Error("theme must apply immediately")
	}
	m.applySetting(item(t, st, "Default display"), "sdl")
	if m.mgr.DefaultDisplay != "sdl" {
		t.Error("default display must reach the manager")
	}
	if _, err := os.Stat(config.Path()); err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	c, err := config.Load()
	if err != nil || c.Theme != "light" || c.DefaultDisplay != "sdl" {
		t.Fatalf("persisted config: %+v %v", c, err)
	}
	m.applySetting(item(t, st, "Raw file editor"), "")
	if !m.statusErr {
		t.Error("empty editor must be rejected")
	}
	iso := filepath.Join(t.TempDir(), "my isos")
	m.applySetting(item(t, st, "ISO folder"), iso)
	if m.mgr.ISODir != iso {
		t.Errorf("ISO dir = %s", m.mgr.ISODir)
	}
}

func TestViewsFitNarrowTerminal(t *testing.T) {
	const w, h = 40, 20
	m := newTestModel(t)
	m.w, m.h = w, h
	m.infos, _ = m.mgr.List()
	m.status = "this is a rather long status message that has to wrap onto more than one line"

	check := func(name string) {
		t.Helper()
		for _, l := range strings.Split(m.View(), "\n") {
			if lipgloss.Width(l) > w {
				t.Errorf("%s: line wider than %d: %q", name, w, l)
			}
		}
	}
	check("list")
	m.selected, m.scr = "a", scMenu
	check("menu")
	m.openConsole("")
	m.addOut("a very long line of console output that must wrap instead of being cut off at the edge")
	m.addOut("error: something went wrong with a long explanation that needs several lines to display")
	check("console")
	m.openVMSettings("a")
	check("settings")
	m.openGlobalSettings()
	check("global settings")
	m.startWizard()
	check("wizard")
	m.scr = scLogs
	m.logLines = []string{strings.Repeat("log text ", 20)}
	m.syncLogs()
	check("logs")
}
