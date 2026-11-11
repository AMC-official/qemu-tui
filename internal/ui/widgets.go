// Package ui is the Bubble Tea front end.
package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/AMC-official/qemu-tui/internal/vm"
)

// Theme holds the terminal colors.
type Theme struct {
	Accent, Dim, OK, Warn, Err, Text lipgloss.Color
}

// NewTheme returns the dark or light theme with optional color overrides
// (keys: accent, dim, ok, warn, err, text; values: ANSI number or #rrggbb).
func NewTheme(name string, custom map[string]string) Theme {
	t := Theme{Accent: "39", Dim: "244", OK: "42", Warn: "214", Err: "196", Text: "252"}
	if name == "light" {
		t = Theme{Accent: "27", Dim: "240", OK: "28", Warn: "166", Err: "160", Text: "235"}
	}
	set := func(dst *lipgloss.Color, key string) {
		if v := custom[key]; v != "" {
			*dst = lipgloss.Color(v)
		}
	}
	set(&t.Accent, "accent")
	set(&t.Dim, "dim")
	set(&t.OK, "ok")
	set(&t.Warn, "warn")
	set(&t.Err, "err")
	set(&t.Text, "text")
	return t
}

func (m *Model) paint(c lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Render(s)
}

func (m *Model) bold(c lipgloss.Color, s string) string {
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(s)
}

// clip truncates a (possibly styled) line to the terminal width.
func (m *Model) clip(s string) string {
	if m.w > 0 {
		return lipgloss.NewStyle().MaxWidth(m.w).Render(s)
	}
	return s
}

func (m *Model) rule() string {
	w := m.w
	if w < 10 {
		w = 40
	}
	return m.paint(m.th.Dim, strings.Repeat("─", w))
}

func (m *Model) header(title string) string {
	return m.bold(m.th.Accent, " "+trunc(title, max(m.w-2, 5))) + "\n" + m.rule() + "\n"
}

// footer renders the rule, the status (max 3 lines) and the key hints (max 2
// lines), all re-wrapped to the current terminal width.
func (m *Model) footer(hints string) string {
	w := max(m.w-2, 10)
	lines := []string{m.rule()}
	if m.status != "" {
		c := m.th.OK
		if m.statusErr {
			c = m.th.Err
		}
		for i, l := range wrapText(m.status, w) {
			if i == 3 {
				break
			}
			lines = append(lines, m.paint(c, " "+l))
		}
	} else {
		lines = append(lines, "")
	}
	for i, l := range wrapText(hints, w) {
		if i == 2 {
			break
		}
		lines = append(lines, m.paint(m.th.Dim, " "+l))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) stateText(s vm.State) string {
	switch s {
	case vm.Running:
		return m.paint(m.th.OK, "● Running")
	case vm.Paused:
		return m.paint(m.th.Warn, "❚❚ Paused")
	}
	return m.paint(m.th.Dim, "○ Stopped")
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

func trunc(s string, w int) string {
	r := []rune(s)
	if w <= 0 {
		return ""
	}
	if len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

// wrapText wraps plain text to w columns. It breaks at spaces when it can,
// hard-breaks long words, and keeps a (small) leading indent on continuation lines.
func wrapText(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	r := []rune(s)
	if len(r) <= w {
		return []string{s}
	}
	indent := 0
	for indent < len(r) && r[indent] == ' ' {
		indent++
	}
	if indent > 8 {
		indent = 8
	}
	if indent > w/2 {
		indent = w / 2
	}
	pad := strings.Repeat(" ", indent)
	var out []string
	first := true
	for len(r) > 0 {
		avail, lead := w, ""
		if !first {
			avail, lead = w-indent, pad
		}
		if len(r) <= avail {
			out = append(out, lead+string(r))
			break
		}
		cut, skip := avail, 0
		for i := avail; i > avail/2; i-- {
			if r[i] == ' ' {
				cut, skip = i, 1
				break
			}
		}
		out = append(out, lead+strings.TrimRight(string(r[:cut]), " "))
		r = r[cut+skip:]
		for len(r) > 0 && r[0] == ' ' {
			r = r[1:]
		}
		first = false
	}
	return out
}
