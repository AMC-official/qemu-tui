package ui

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/AMC-official/qemu-tui/internal/config"
	"github.com/AMC-official/qemu-tui/internal/console"
	"github.com/AMC-official/qemu-tui/internal/storage"
	"github.com/AMC-official/qemu-tui/internal/system"
	"github.com/AMC-official/qemu-tui/internal/vm"
)

type screen int

const (
	scList screen = iota
	scMenu
	scConsole
	scWizard
	scLogs
	scTop
	scSnaps
	scPrompt
	scSettings
)

var menuItems = []string{"Start", "Stop", "Force Stop", "Restart", "Pause", "Resume", "Settings",
	"Console", "Logs", "Snapshots", "Attach Media", "Clone", "Delete"}

// ---- messages ----

type tickMsg time.Time

type dataMsg struct {
	infos []vm.Info
	stats map[string]system.Stats
}

type resultMsg struct {
	msg   string
	err   error
	vm    string   // VM whose console should receive lines
	lines []string // log lines to show in that console
}

type consoleOutMsg struct {
	out string
	err error
}

type snapsMsg struct {
	snaps []storage.Snapshot
	err   error
}

type logsMsg struct {
	lines []string
	err   error
}

type editorDoneMsg struct {
	vm      string
	global  bool
	err     error
}

// Model is the root Bubble Tea model.
type Model struct {
	mgr     *vm.Manager
	cfg     config.Config
	th      Theme
	con     *console.Console
	hist    *console.History
	sampler *system.Sampler

	w, h      int
	scr       screen
	infos     []vm.Info
	stats     map[string]system.Stats
	cursor    int
	filter    string
	searching bool
	search    textinput.Model
	status    string
	statusErr bool

	menuCursor int
	selected   string

	input  textinput.Model
	out    []string
	scroll int
	conVM  string

	wiz *wizard

	vp       viewport.Model
	logVM    string
	logLines []string

	set *settingsState

	snaps      []storage.Snapshot
	snapCursor int

	prompt      textinput.Model
	promptLabel string
	promptFn    func(string) tea.Cmd
	promptBack  screen
}

// New builds the model.
func New(mgr *vm.Manager, cfg config.Config) *Model {
	in := textinput.New()
	in.Prompt = ":"
	in.CharLimit = 512
	se := textinput.New()
	se.Prompt = "/"
	se.CharLimit = 64
	pr := textinput.New()
	pr.Prompt = "> "
	pr.CharLimit = 256
	return &Model{
		mgr: mgr, cfg: cfg, th: NewTheme(cfg.Theme, cfg.Colors),
		con:     &console.Console{Mgr: mgr},
		hist:    console.LoadHistory(config.HistoryPath(), 500),
		sampler: system.NewSampler(),
		w:       80, h: 24,
		stats: map[string]system.Stats{},
		input: in, search: se, prompt: pr,
		vp: viewport.New(80, 20),
	}
}

// Run starts the TUI.
func Run(mgr *vm.Manager, cfg config.Config, openConsole bool) error {
	m := New(mgr, cfg)
	if openConsole {
		m.openConsole("")
	}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

func (m *Model) key(action string) string {
	if k := m.cfg.Keybindings[action]; k != "" {
		return k
	}
	return config.DefaultKeybindings()[action]
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status = strings.SplitN(s, "\n", 2)[0]
	m.statusErr = isErr
}

// ---- commands ----

func (m *Model) loadCmd() tea.Cmd {
	mgr, s := m.mgr, m.sampler
	return func() tea.Msg {
		infos, _ := mgr.List()
		stats := map[string]system.Stats{}
		for _, in := range infos {
			if in.PID != 0 {
				if st, err := s.Sample(in.PID); err == nil {
					stats[in.Config.Name] = st
				}
			}
		}
		return dataMsg{infos: infos, stats: stats}
	}
}

func (m *Model) tickEvery() tea.Cmd {
	d := 2 * time.Second
	if m.scr == scTop {
		d = time.Second
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// act runs f off the UI thread and reports the result.
func (m *Model) act(okMsg string, f func() error) tea.Cmd {
	return func() tea.Msg {
		if err := f(); err != nil {
			return resultMsg{err: err}
		}
		return resultMsg{msg: okMsg}
	}
}

// actLog is act for a VM whose console should show the resulting log lines.
func (m *Model) actLog(name, okMsg string, f func() error) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		err := f()
		lines, _ := mgr.Logs(name, 8)
		if err != nil {
			return resultMsg{err: err, vm: name, lines: lines}
		}
		return resultMsg{msg: okMsg, vm: name, lines: lines}
	}
}

func (m *Model) snapsCmd(name string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		s, err := mgr.Snapshots(name)
		return snapsMsg{snaps: s, err: err}
	}
}

func (m *Model) logsCmd(name string) tea.Cmd {
	mgr := m.mgr
	return func() tea.Msg {
		l, err := mgr.Logs(name, 500)
		return logsMsg{lines: l, err: err}
	}
}

func (m *Model) editFile(path, name string, global bool) tea.Cmd {
	parts := strings.Fields(m.cfg.Editor)
	if len(parts) == 0 {
		m.setStatus("no editor configured (set \"editor\" in config.json)", true)
		return nil
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{vm: name, global: global, err: err}
	})
}

// ---- Init / Update ----

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.loadCmd(), m.tickEvery()) }

func (m *Model) clamp() {
	n := len(m.visible())
	if m.cursor >= n {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) visible() []vm.Info {
	if m.filter == "" {
		return m.infos
	}
	var out []vm.Info
	for _, in := range m.infos {
		if strings.Contains(strings.ToLower(in.Config.Name), strings.ToLower(m.filter)) {
			out = append(out, in)
		}
	}
	return out
}

func (m *Model) addOut(s string) {
	for _, l := range strings.Split(strings.TrimRight(s, "\r\n"), "\n") {
		m.out = append(m.out, strings.TrimRight(l, "\r"))
	}
	if len(m.out) > 1000 {
		m.out = m.out[len(m.out)-1000:]
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.vp.Width = m.w
		m.syncLogs()
		m.input.Width = max(m.w-4, 10)
		m.search.Width = max(m.w-4, 10)
		m.prompt.Width = max(m.w-6, 10)
		return m, nil
	case tickMsg:
		return m, tea.Batch(m.loadCmd(), m.tickEvery())
	case dataMsg:
		m.infos, m.stats = msg.infos, msg.stats
		m.clamp()
		return m, nil
	case resultMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
		} else if msg.msg != "" {
			m.setStatus(msg.msg, false)
		}
		if m.scr == scConsole && msg.vm != "" && msg.vm == m.conVM {
			for _, l := range msg.lines {
				m.addOut(l)
			}
			if msg.err != nil {
				m.addOut("error: " + msg.err.Error())
			}
		}
		cmds := []tea.Cmd{m.loadCmd()}
		if m.scr == scSnaps {
			cmds = append(cmds, m.snapsCmd(m.selected))
		}
		return m, tea.Batch(cmds...)
	case consoleOutMsg:
		if errors.Is(msg.err, console.ErrExit) {
			m.scr = scList
			m.input.Blur()
			return m, m.loadCmd()
		}
		if msg.err != nil {
			m.addOut("error: " + msg.err.Error())
		} else if msg.out != "" {
			m.addOut(msg.out)
		}
		m.scroll = 0
		return m, m.loadCmd()
	case snapsMsg:
		if msg.err != nil {
			m.setStatus(msg.err.Error(), true)
			m.snaps = nil
		} else {
			m.snaps = msg.snaps
		}
		if m.snapCursor >= len(m.snaps) {
			m.snapCursor = max(len(m.snaps)-1, 0)
		}
		return m, nil
	case logsMsg:
		m.logLines = msg.lines
		if msg.err != nil || len(msg.lines) == 0 {
			m.logLines = []string{"(no log yet)"}
		}
		m.syncLogs()
		m.vp.GotoBottom()
		return m, nil
	case editorDoneMsg:
		switch {
		case msg.err != nil:
			m.setStatus("editor failed: "+msg.err.Error(), true)
		case msg.global:
			m.reloadGlobal()
		default:
			if _, err := m.mgr.Load(msg.vm); err != nil {
				m.setStatus("config problem: "+err.Error(), true)
			} else {
				m.reloadSettings()
				m.setStatus(msg.vm+": config saved (applies on next start)", false)
			}
		}
		return m, m.loadCmd()
	case tea.KeyMsg:
		return m.onKey(msg)
	}
	return m, nil
}

func (m *Model) onKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" || s == m.key("quit") {
		return m, tea.Quit
	}
	switch m.scr {
	case scList:
		return m.keyList(k)
	case scMenu:
		return m.keyMenu(k)
	case scConsole:
		return m.keyConsole(k)
	case scWizard:
		return m.keyWizard(k)
	case scLogs:
		return m.keyLogs(k)
	case scTop:
		if s == "esc" || s == "q" {
			m.scr = scList
		}
		return m, nil
	case scSnaps:
		return m.keySnaps(k)
	case scPrompt:
		return m.keyPrompt(k)
	case scSettings:
		return m.keySettings(k)
	}
	return m, nil
}

// ---- list ----

func (m *Model) keyList(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.searching {
		switch s {
		case "enter":
			m.searching = false
			m.search.Blur()
		case "esc":
			m.searching, m.filter = false, ""
			m.search.SetValue("")
			m.search.Blur()
		default:
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(k)
			m.filter = m.search.Value()
			m.cursor = 0
			return m, cmd
		}
		return m, nil
	}
	vis := m.visible()
	switch s {
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "enter":
		if len(vis) > 0 {
			m.selected = vis[m.cursor].Config.Name
			m.menuCursor = 0
			m.scr = scMenu
		}
	case "esc":
		m.filter = ""
		m.search.SetValue("")
	case m.key("search"):
		m.searching = true
		m.search.Focus()
	case m.key("new"):
		m.startWizard()
	case m.key("refresh"):
		return m, m.loadCmd()
	case ":", m.key("console"):
		m.openConsole("")
	case m.key("top"):
		m.scr = scTop
	case "s", "e":
		m.openGlobalSettings()
	}
	m.clamp()
	return m, nil
}

func (m *Model) viewList() string {
	var b strings.Builder
	b.WriteString(m.header("QEMU-TUI"))
	if m.searching || m.filter != "" {
		b.WriteString(" " + m.search.View() + "\n")
	}
	vis := m.visible()
	if len(vis) == 0 {
		b.WriteString("\n")
		for _, l := range wrapText("No VMs. Press "+m.key("new")+" to create one.", max(m.w-4, 10)) {
			b.WriteString(m.paint(m.th.Dim, "  "+l) + "\n")
		}
	}
	for i, in := range vis {
		c := in.Config
		up, disk := "-", c.DiskSize
		if in.State != vm.Stopped {
			up = system.HumanDuration(in.Uptime)
		}
		if disk == "" {
			disk = "-"
		}
		mark := "  "
		if i == m.cursor {
			mark = m.bold(m.th.Accent, "▸ ")
		}
		nameW := 20
		if m.w < 60 {
			nameW = max(m.w-16, 6)
		}
		line := fmt.Sprintf("%s%-*s %s", mark, nameW, trunc(c.Name, nameW), m.stateText(in.State))
		if m.w >= 52 {
			line += fmt.Sprintf("   %3d CPU %6d MB", c.CPUs, c.Memory)
		}
		if m.w >= 72 {
			line += fmt.Sprintf("  %6s  net:%-4s", disk, c.NetworkMode)
		}
		if m.w >= 90 {
			line += "  up " + up
		}
		b.WriteString(m.clip(line) + "\n")
	}
	b.WriteString("\n")
	b.WriteString(m.footer("↑↓ move · enter menu · / search · " + m.key("new") + " new · : console · t top · s settings · " +
		m.key("refresh") + " refresh · " + m.key("quit") + " quit"))
	return b.String()
}

// ---- per-VM menu ----

func (m *Model) keyMenu(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.scr = scList
	case "up", "k":
		m.menuCursor = max(m.menuCursor-1, 0)
	case "down", "j":
		m.menuCursor = min(m.menuCursor+1, len(menuItems)-1)
	case "enter":
		return m.menuAction(menuItems[m.menuCursor])
	}
	return m, nil
}

func (m *Model) menuAction(item string) (tea.Model, tea.Cmd) {
	name, mgr := m.selected, m.mgr
	back := func(msg string, f func() error) (tea.Model, tea.Cmd) {
		m.scr = scList
		return m, m.act(msg, f)
	}
	switch item {
	case "Start":
		m.openConsole(name)
		m.addOut("[" + time.Now().Format("15:04:05") + "] Starting QEMU...")
		return m, m.actLog(name, name+" started", func() error { return mgr.Start(name) })
	case "Stop":
		return back(name+": power-down requested", func() error { return mgr.Stop(name) })
	case "Force Stop":
		return back(name+" force stopped", func() error { return mgr.ForceStop(name) })
	case "Restart":
		return back(name+" reset", func() error { return mgr.Restart(name) })
	case "Pause":
		return back(name+" paused", func() error { return mgr.Pause(name) })
	case "Resume":
		return back(name+" resumed", func() error { return mgr.Resume(name) })
	case "Settings":
		m.openVMSettings(name)
	case "Console":
		m.openConsole(name)
	case "Logs":
		m.scr, m.logVM = scLogs, name
		m.vp.SetContent("")
		return m, m.logsCmd(name)
	case "Snapshots":
		m.scr, m.snapCursor = scSnaps, 0
		return m, m.snapsCmd(name)
	case "Attach Media":
		m.openPrompt("ISO (name in "+mgr.ISODir+" or full path; empty = eject)", scList, func(v string) tea.Cmd {
			v = strings.TrimSpace(v)
			if v == "" {
				return m.act(name+": ISO ejected", func() error { return mgr.EjectISO(name) })
			}
			return m.act(name+": ISO attached", func() error { return mgr.AttachISO(name, v) })
		})
	case "Clone":
		m.openPrompt("Name for the clone of "+name, scList, func(v string) tea.Cmd {
			v = strings.TrimSpace(v)
			return m.act("Cloned to "+v, func() error { return mgr.Clone(name, v) })
		})
	case "Delete":
		m.openPrompt("Type \""+name+"\" to delete it and all its files", scList, func(v string) tea.Cmd {
			if strings.TrimSpace(v) != name {
				m.setStatus("delete cancelled", false)
				return nil
			}
			return m.act(name+" deleted", func() error { return mgr.Delete(name) })
		})
	}
	return m, nil
}

func (m *Model) viewMenu() string {
	var b strings.Builder
	state := vm.Stopped
	for _, in := range m.infos {
		if in.Config.Name == m.selected {
			state = in.State
		}
	}
	b.WriteString(m.header("VM Name: " + m.selected + "   " + string(state)))
	for i, it := range menuItems {
		if i == m.menuCursor {
			b.WriteString(m.bold(m.th.Accent, " ▸ "+it) + "\n")
		} else {
			b.WriteString("   " + it + "\n")
		}
	}
	b.WriteString("\n" + m.footer("↑↓ move · enter select · esc back"))
	return b.String()
}

// ---- prompt ----

func (m *Model) openPrompt(label string, back screen, fn func(string) tea.Cmd) {
	m.promptLabel, m.promptFn, m.promptBack = label, fn, back
	m.prompt.SetValue("")
	m.prompt.Focus()
	m.scr = scPrompt
}

func (m *Model) keyPrompt(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.scr = m.promptBack
		return m, nil
	case "enter":
		m.scr = m.promptBack
		m.prompt.Blur()
		if m.promptFn != nil {
			return m, m.promptFn(m.prompt.Value())
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.prompt, cmd = m.prompt.Update(k)
	return m, cmd
}

func (m *Model) viewPrompt() string {
	var b strings.Builder
	b.WriteString(m.header("Input"))
	for _, l := range wrapText(m.promptLabel, max(m.w-3, 10)) {
		b.WriteString(m.bold(m.th.Text, " "+l) + "\n")
	}
	b.WriteString("\n " + m.prompt.View() + "\n\n" + m.footer("enter confirm · esc cancel"))
	return b.String()
}

// ---- console ----

func (m *Model) openConsole(name string) {
	m.conVM = name
	m.con.Scope = name
	m.out = nil
	m.addOut(m.con.Banner())
	if name != "" {
		if lines, err := m.mgr.Logs(name, 10); err == nil {
			for _, l := range lines {
				m.addOut(l)
			}
		}
	}
	m.input.SetValue("")
	m.input.Focus()
	m.scroll = 0
	m.scr = scConsole
}

func (m *Model) keyConsole(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.scr = scList
		m.input.Blur()
		return m, m.loadCmd()
	case "enter":
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(m.input.Value()), ":"))
		m.input.SetValue("")
		if line == "" {
			return m, nil
		}
		m.hist.Add(line)
		m.addOut(":" + line)
		fields := strings.Fields(line)
		switch strings.ToLower(fields[0]) {
		case "top":
			m.scr = scTop
			return m, nil
		case "clear":
			m.out = nil
			return m, nil
		case "create":
			if len(fields) == 1 {
				m.startWizard()
				return m, nil
			}
		}
		con := &console.Console{Mgr: m.mgr, Scope: m.conVM, Sampler: m.sampler}
		return m, func() tea.Msg {
			out, err := con.Execute(line)
			return consoleOutMsg{out: out, err: err}
		}
	case "up":
		m.input.SetValue(m.hist.Prev())
		m.input.CursorEnd()
	case "down":
		m.input.SetValue(m.hist.Next())
		m.input.CursorEnd()
	case "tab":
		cands := m.con.Complete(m.input.Value())
		if len(cands) > 1 {
			m.addOut(strings.Join(cands, "  "))
		}
		m.input.SetValue(console.ApplyCompletion(m.input.Value(), cands))
		m.input.CursorEnd()
	case "pgup":
		m.scroll += max(m.h/2, 1)
	case "pgdown":
		m.scroll = max(m.scroll-max(m.h/2, 1), 0)
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(k)
		return m, cmd
	}
	return m, nil
}

func (m *Model) viewConsole() string {
	title := "Global Console"
	if m.conVM != "" {
		title = m.conVM + " Console"
	}
	foot := m.footer("enter run · tab complete · ↑↓ history · pgup/pgdn scroll · esc close")
	avail := max(m.h-5-strings.Count(foot, "\n"), 3)

	type vline struct {
		text string
		kind int
	}
	var vl []vline
	for _, l := range m.out {
		kind := 0
		switch {
		case strings.HasPrefix(l, ":"):
			kind = 1
		case strings.HasPrefix(l, "error:"):
			kind = 2
		}
		for _, p := range wrapText(l, max(m.w-2, 8)) {
			vl = append(vl, vline{p, kind})
		}
	}
	end := max(len(vl)-m.scroll, 0)
	start := max(end-avail, 0)

	var b strings.Builder
	b.WriteString(m.header(title))
	for _, l := range vl[start:end] {
		t := " " + l.text
		switch l.kind {
		case 1:
			t = m.paint(m.th.Accent, t)
		case 2:
			t = m.paint(m.th.Err, t)
		}
		b.WriteString(t + "\n")
	}
	for i := end - start; i < avail; i++ {
		b.WriteString("\n")
	}
	b.WriteString(m.rule() + "\n " + m.input.View() + "\n" + foot)
	return b.String()
}

// ---- logs ----

func (m *Model) keyLogs(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.scr = scList
		return m, nil
	case "r":
		return m, m.logsCmd(m.logVM)
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(k)
	return m, cmd
}

// syncLogs re-wraps the log to the current width.
func (m *Model) syncLogs() {
	var vis []string
	for _, l := range m.logLines {
		vis = append(vis, wrapText(l, max(m.w-2, 8))...)
	}
	m.vp.SetContent(strings.Join(vis, "\n"))
}

func (m *Model) viewLogs() string {
	foot := m.footer("↑↓/pgup/pgdn scroll · r reload · esc back")
	m.vp.Width = m.w
	m.vp.Height = max(m.h-2-1-strings.Count(foot, "\n"), 3)
	return m.header("Logs: "+m.logVM) + m.vp.View() + "\n" + foot
}

// ---- top ----

func (m *Model) viewTop() string {
	var b strings.Builder
	n := 0
	for _, in := range m.infos {
		if in.PID != 0 {
			n++
		}
	}
	b.WriteString(m.header(fmt.Sprintf("Resource monitor - %d running", n)))
	if n == 0 {
		b.WriteString(m.paint(m.th.Dim, "\n  No running VMs.\n"))
	}
	for _, in := range m.infos {
		st, ok := m.stats[in.Config.Name]
		if in.PID == 0 || !ok {
			continue
		}
		cpu := st.CPUPercent / float64(in.Config.CPUs)
		ram := float64(st.RSSBytes) / (float64(in.Config.Memory) * 1024 * 1024) * 100
		b.WriteString("\n " + m.bold(m.th.Text, in.Config.Name) + "  " + m.stateText(in.State) + "\n")
		b.WriteString(fmt.Sprintf("   CPU: %s %3.0f%%\n", m.paint(m.th.Accent, bar(cpu, 24)), cpu))
		b.WriteString(fmt.Sprintf("   RAM: %s %3.0f%%  %s\n", m.paint(m.th.Accent, bar(ram, 24)), ram, system.HumanBytes(st.RSSBytes)))
		b.WriteString(fmt.Sprintf("   Disk IO: ↓ %s/s  ↑ %s/s   Net: %s\n",
			system.HumanBytes(uint64(st.ReadRate)), system.HumanBytes(uint64(st.WriteRate)), in.Config.NetworkMode))
	}
	b.WriteString("\n" + m.footer("esc back"))
	return b.String()
}

// ---- snapshots ----

func (m *Model) keySnaps(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	name, mgr := m.selected, m.mgr
	switch k.String() {
	case "esc", "q":
		m.scr = scMenu
	case "up", "k":
		m.snapCursor = max(m.snapCursor-1, 0)
	case "down", "j":
		m.snapCursor = min(m.snapCursor+1, max(len(m.snaps)-1, 0))
	case "n":
		m.openPrompt("Snapshot name", scSnaps, func(v string) tea.Cmd {
			v = strings.TrimSpace(v)
			return m.act("Snapshot "+v+" created", func() error { return mgr.CreateSnapshot(name, v) })
		})
	case "enter", "r":
		if len(m.snaps) > 0 {
			snap := m.snaps[m.snapCursor].Name
			return m, m.act("Restored "+snap, func() error { return mgr.RestoreSnapshot(name, snap) })
		}
	case "d":
		if len(m.snaps) > 0 {
			snap := m.snaps[m.snapCursor].Name
			return m, m.act("Deleted "+snap, func() error { return mgr.DeleteSnapshot(name, snap) })
		}
	}
	return m, nil
}

func (m *Model) viewSnaps() string {
	var b strings.Builder
	b.WriteString(m.header("Snapshots: " + m.selected))
	if len(m.snaps) == 0 {
		b.WriteString(m.paint(m.th.Dim, "\n  No snapshots (qcow2 disks only). Press n to create one.\n"))
	}
	for i, s := range m.snaps {
		mark := "   "
		if i == m.snapCursor {
			mark = m.bold(m.th.Accent, " ▸ ")
		}
		b.WriteString(fmt.Sprintf("%s%-28s %s\n", mark, trunc(s.Name, 28), s.Created().Format("2006-01-02 15:04")))
	}
	b.WriteString("\n" + m.footer("n new · enter restore · d delete · esc back (VM must be stopped)"))
	return b.String()
}

// ---- wizard ----

type field struct {
	label   string
	choices []string
	text    string
	idx     int
	hint    string
}

type wizard struct {
	fields []field
	step   int
	input  textinput.Model
	err    string
}

func (w *wizard) value(label string) string {
	for _, f := range w.fields {
		if f.label == label {
			if f.choices != nil {
				return f.choices[f.idx]
			}
			return f.text
		}
	}
	return ""
}

func (w *wizard) loadStep() {
	f := w.fields[w.step]
	w.err = ""
	if f.choices == nil {
		w.input.SetValue(f.text)
		w.input.CursorEnd()
		w.input.Focus()
	} else {
		w.input.Blur()
	}
}

func (m *Model) startWizard() {
	disp := 0
	for i, d := range vm.Displays {
		if d == m.cfg.DefaultDisplay {
			disp = i
		}
	}
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 256
	m.wiz = &wizard{input: in, fields: []field{
		{label: "Name", hint: "letters, digits, . _ -"},
		{label: "Architecture", choices: vm.Architectures},
		{label: "RAM (MiB)", text: "2048"},
		{label: "CPU cores", text: "2"},
		{label: "Disk size", text: "20G", hint: "e.g. 20G"},
		{label: "Disk format", choices: vm.DiskFormats},
		{label: "Firmware", choices: vm.Firmwares, hint: "non-x86 guests always use UEFI"},
		{label: "Network", choices: vm.NetModes},
		{label: "TAP interface", text: "tap0", hint: "only used with TAP networking"},
		{label: "Display", choices: vm.Displays, idx: disp},
		{label: "ISO", hint: "name in " + m.mgr.ISODir + " or a full path; empty = none"},
	}}
	m.wiz.loadStep()
	m.scr = scWizard
}

func (m *Model) checkField(label, v string) error {
	switch label {
	case "Name":
		if !vm.ValidName(v) {
			return errors.New("invalid name (letters, digits, . _ - ; max 63)")
		}
		for _, n := range m.mgr.Names() {
			if n == v {
				return errors.New("a VM with that name already exists")
			}
		}
	case "RAM (MiB)":
		if n, err := strconv.Atoi(v); err != nil || n < 16 {
			return errors.New("enter a number of MiB (at least 16)")
		}
	case "CPU cores":
		if n, err := strconv.Atoi(v); err != nil || n < 1 {
			return errors.New("enter a number of cores (at least 1)")
		}
	case "Disk size":
		if !storage.ValidSize(v) {
			return errors.New("invalid size, example: 20G")
		}
	}
	return nil
}

func (m *Model) keyWizard(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	w := m.wiz
	f := &w.fields[w.step]
	s := k.String()
	switch s {
	case "esc":
		m.wiz, m.scr = nil, scList
		return m, nil
	case "shift+tab", "ctrl+p":
		w.step--
		if w.step >= 0 && w.fields[w.step].label == "TAP interface" && w.value("Network") != "tap" {
			w.step--
		}
		if w.step < 0 {
			w.step = 0
		}
		w.loadStep()
		return m, nil
	case "enter":
		return m, m.wizNext()
	}
	if f.choices != nil {
		switch s {
		case "left", "h", "up", "k":
			f.idx = (f.idx + len(f.choices) - 1) % len(f.choices)
		case "right", "l", "down", "j", "tab", " ":
			f.idx = (f.idx + 1) % len(f.choices)
		}
		return m, nil
	}
	var cmd tea.Cmd
	w.input, cmd = w.input.Update(k)
	return m, cmd
}

func (m *Model) wizNext() tea.Cmd {
	w := m.wiz
	f := &w.fields[w.step]
	if f.choices == nil {
		v := strings.TrimSpace(w.input.Value())
		if err := m.checkField(f.label, v); err != nil {
			w.err = err.Error()
			return nil
		}
		f.text = v
	}
	w.step++
	if w.step < len(w.fields) && w.fields[w.step].label == "TAP interface" && w.value("Network") != "tap" {
		w.step++
	}
	if w.step < len(w.fields) {
		w.loadStep()
		return nil
	}
	cfg := vm.Config{
		Name: w.value("Name"), Architecture: w.value("Architecture"),
		DiskFormat: w.value("Disk format"), DiskSize: w.value("Disk size"),
		Firmware: w.value("Firmware"), NetworkMode: w.value("Network"),
		TapIface: w.value("TAP interface"), Display: w.value("Display"), ISO: w.value("ISO"),
	}
	cfg.Memory, _ = strconv.Atoi(w.value("RAM (MiB)"))
	cfg.CPUs, _ = strconv.Atoi(w.value("CPU cores"))
	if !cfg.IsX86() {
		cfg.Firmware = "uefi"
	}
	m.wiz, m.scr = nil, scList
	mgr := m.mgr
	return m.act("Created "+cfg.Name, func() error { return mgr.Create(cfg) })
}

func (m *Model) viewWizard() string {
	w := m.wiz
	var b strings.Builder
	b.WriteString(m.header("Create VM"))
	for i, f := range w.fields {
		if f.label == "TAP interface" && w.value("Network") != "tap" {
			continue
		}
		val := w.value(f.label)
		mark := "   "
		switch {
		case i == w.step:
			mark = m.bold(m.th.Accent, " ▸ ")
			if f.choices != nil {
				val = m.bold(m.th.Text, "◀ "+val+" ▶")
			} else {
				val = w.input.View()
			}
		case i > w.step:
			val = m.paint(m.th.Dim, val)
		}
		b.WriteString(m.clip(fmt.Sprintf("%s%-14s %s", mark, f.label+":", val)) + "\n")
	}
	if h := w.fields[w.step].hint; h != "" {
		b.WriteString("\n")
		for _, l := range wrapText(h, max(m.w-4, 10)) {
			b.WriteString(m.paint(m.th.Dim, "   "+l) + "\n")
		}
	}
	if w.err != "" {
		b.WriteString("\n")
		for _, l := range wrapText(w.err, max(m.w-4, 10)) {
			b.WriteString(m.paint(m.th.Err, "   "+l) + "\n")
		}
	}
	b.WriteString("\n" + m.footer("enter next · shift+tab back · ←/→ change choice · esc cancel"))
	return b.String()
}

// View implements tea.Model.
func (m *Model) View() string {
	switch m.scr {
	case scMenu:
		return m.viewMenu()
	case scConsole:
		return m.viewConsole()
	case scWizard:
		return m.viewWizard()
	case scLogs:
		return m.viewLogs()
	case scTop:
		return m.viewTop()
	case scSnaps:
		return m.viewSnaps()
	case scPrompt:
		return m.viewPrompt()
	case scSettings:
		return m.viewSettings()
	}
	return m.viewList()
}
