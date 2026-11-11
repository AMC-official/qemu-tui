package console

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AMC-official/qemu-tui/internal/vm"
)

func newConsole(t *testing.T) *Console {
	d := t.TempDir()
	m := vm.NewManager(filepath.Join(d, "vms"), filepath.Join(d, "logs"), filepath.Join(d, "isos"))
	return &Console{Mgr: m}
}

func mkVM(t *testing.T, c *Console, name string) {
	t.Helper()
	dir := filepath.Join(c.Mgr.Root, name)
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	cfg := vm.Config{Name: name}
	cfg.ApplyDefaults()
	if err := vm.SaveConfig(filepath.Join(dir, "config.json"), cfg); err != nil {
		t.Fatal(err)
	}
}

func TestTokenize(t *testing.T) {
	cases := map[string][]string{
		`start debian`:             {"start", "debian"},
		`  a   b  `:                {"a", "b"},
		`monitor vm "info status"`: {"monitor", "vm", "info status"},
		`x 'a b' c\ d`:             {"x", "a b", "c d"},
		`echo ""`:                  {"echo", ""},
		`create vm; rm -rf /`:      {"create", "vm;", "rm", "-rf", "/"},
	}
	for in, want := range cases {
		got, err := Tokenize(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("Tokenize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := Tokenize(`a "b`); err == nil {
		t.Error("unterminated quote must error")
	}
}

func TestExecuteBasics(t *testing.T) {
	c := newConsole(t)
	out, err := c.Execute(":help")
	if err != nil || !strings.Contains(out, ":start <vm>") || !strings.Contains(out, ":usb") {
		t.Fatalf("help: %q %v", out, err)
	}
	if out, _ := c.Execute("list"); !strings.Contains(out, "No VMs") {
		t.Errorf("empty list: %q", out)
	}
	if _, err := c.Execute("frobnicate"); err == nil {
		t.Error("unknown command must error")
	}
	if _, err := c.Execute("start"); err == nil {
		t.Error("missing vm must error")
	}
	if _, err := c.Execute("exit"); !errors.Is(err, ErrExit) {
		t.Errorf("exit: %v", err)
	}
	if _, err := c.Execute("quit"); !errors.Is(err, ErrExit) {
		t.Errorf("quit: %v", err)
	}
	if out, err := c.Execute("   "); out != "" || err != nil {
		t.Error("blank line is a no-op")
	}
}

func TestListStatusAndScope(t *testing.T) {
	c := newConsole(t)
	mkVM(t, c, "debian")
	mkVM(t, c, "arch")
	out, _ := c.Execute("list")
	if !strings.Contains(out, "debian") || !strings.Contains(out, "Stopped") {
		t.Fatalf("list: %q", out)
	}
	out, err := c.Execute("status debian")
	if err != nil || !strings.Contains(out, "State:     Stopped") {
		t.Fatalf("status: %q %v", out, err)
	}
	// per-VM scope: vm argument is implied
	sc := &Console{Mgr: c.Mgr, Scope: "debian"}
	if out, err := sc.Execute("status"); err != nil || !strings.Contains(out, "debian") {
		t.Fatalf("scoped status: %q %v", out, err)
	}
	if _, err := sc.Execute("stop"); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("scoped stop: %v", err)
	}
	if out, _ := c.Execute("top"); !strings.Contains(out, "No running VMs") {
		t.Errorf("top: %q", out)
	}
}

func TestConfigCommands(t *testing.T) {
	c := newConsole(t)
	mkVM(t, c, "a")
	if _, err := c.Execute("boot a cdrom"); err != nil {
		t.Fatal(err)
	}
	if cfg, _ := c.Mgr.Load("a"); cfg.Boot != "cdrom" {
		t.Errorf("boot = %s", cfg.Boot)
	}
	if _, err := c.Execute("boot a floppy"); err == nil {
		t.Error("invalid boot device must error")
	}
	if _, err := c.Execute("usb attach a 1234:abcd"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Execute("usb attach a nonsense"); err == nil {
		t.Error("bad usb id must error")
	}
	if _, err := c.Execute("create x mem=abc"); err == nil {
		t.Error("bad number must error")
	}
	if _, err := c.Execute("create x bogus=1"); err == nil {
		t.Error("unknown option must error")
	}
	if _, err := c.Execute("iso attach a ../../etc/passwd"); err == nil {
		t.Error("traversal must error")
	}
	if out, err := c.Execute("logs"); err != nil || out == "" {
		t.Errorf("logs: %q %v", out, err)
	}
}

func TestCompletion(t *testing.T) {
	c := newConsole(t)
	mkVM(t, c, "debian")
	mkVM(t, c, "devuan")
	mkVM(t, c, "arch")
	if got := c.Complete("sta"); !reflect.DeepEqual(got, []string{"start", "status"}) {
		t.Errorf("commands: %v", got)
	}
	if got := c.Complete("start de"); !reflect.DeepEqual(got, []string{"debian", "devuan"}) {
		t.Errorf("vms: %v", got)
	}
	if got := c.Complete("start "); len(got) != 3 {
		t.Errorf("all vms: %v", got)
	}
	if got := c.Complete("usb a"); !reflect.DeepEqual(got, []string{"attach"}) {
		t.Errorf("sub: %v", got)
	}
	if got := c.Complete("boot arch c"); !reflect.DeepEqual(got, []string{"cdrom"}) {
		t.Errorf("boot: %v", got)
	}
	if CommonPrefix([]string{"debian", "devuan"}) != "de" {
		t.Error("CommonPrefix")
	}
	if got := ApplyCompletion("start de", []string{"debian", "devuan"}); got != "start de" {
		t.Errorf("apply: %q", got)
	}
	if got := ApplyCompletion("start deb", []string{"debian"}); got != "start debian " {
		t.Errorf("apply single: %q", got)
	}
	if got := ApplyCompletion(":sta", []string{"start", "status"}); got != ":sta" {
		t.Errorf("apply colon: %q", got)
	}
	sc := &Console{Mgr: c.Mgr, Scope: "arch"}
	if got := sc.Complete("boot d"); !reflect.DeepEqual(got, []string{"disk"}) {
		t.Errorf("scoped boot: %v", got)
	}
}

func TestHistory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "history")
	h := LoadHistory(p, 3)
	h.Add("list")
	h.Add("list") // duplicate collapsed
	h.Add("start a")
	h.Add("stop a")
	h.Add("status") // oldest dropped (max 3)
	if got := h.Items(); !reflect.DeepEqual(got, []string{"start a", "stop a", "status"}) {
		t.Fatalf("items: %v", got)
	}
	if h.Prev() != "status" || h.Prev() != "stop a" || h.Prev() != "start a" || h.Prev() != "start a" {
		t.Error("Prev navigation")
	}
	if h.Next() != "stop a" || h.Next() != "status" || h.Next() != "" {
		t.Error("Next navigation")
	}
	h2 := LoadHistory(p, 3) // persisted
	if len(h2.Items()) != 3 {
		t.Errorf("persisted: %v", h2.Items())
	}
	h.Add("bad\nline") // never stored
	if len(h.Items()) != 3 {
		t.Error("multi-line entries must be rejected")
	}
}
