package console

import (
	"os"
	"path/filepath"
	"strings"
)

// History is a persistent command history with up/down navigation.
type History struct {
	items []string
	pos   int
	path  string
	max   int
}

// LoadHistory reads the history file (missing file = empty history).
func LoadHistory(path string, max int) *History {
	h := &History{path: path, max: max}
	if b, err := os.ReadFile(path); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(l) != "" {
				h.items = append(h.items, l)
			}
		}
	}
	if len(h.items) > max {
		h.items = h.items[len(h.items)-max:]
	}
	h.pos = len(h.items)
	return h
}

// Items returns a copy of the history.
func (h *History) Items() []string { return append([]string(nil), h.items...) }

// Add records a command (consecutive duplicates are collapsed) and saves.
func (h *History) Add(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return
	}
	if n := len(h.items); n == 0 || h.items[n-1] != line {
		h.items = append(h.items, line)
		if len(h.items) > h.max {
			h.items = h.items[len(h.items)-h.max:]
		}
		h.save()
	}
	h.pos = len(h.items)
}

// Prev moves to the previous entry; at the oldest entry it stays put.
func (h *History) Prev() string {
	if len(h.items) == 0 {
		return ""
	}
	if h.pos > 0 {
		h.pos--
	}
	return h.items[h.pos]
}

// Next moves to the next entry; past the newest it returns "".
func (h *History) Next() string {
	if h.pos < len(h.items)-1 {
		h.pos++
		return h.items[h.pos]
	}
	h.pos = len(h.items)
	return ""
}

func (h *History) save() {
	if h.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o755); err != nil {
		return
	}
	os.WriteFile(h.path, []byte(strings.Join(h.items, "\n")+"\n"), 0o600)
}
