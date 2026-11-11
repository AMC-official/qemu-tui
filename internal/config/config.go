// Package config handles the global qemu-tui configuration file.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Config is the global configuration (~/.config/qemu-tui/config.json).
type Config struct {
	DefaultDisplay string            `json:"default_display"`
	VMDir          string            `json:"vm_dir"`
	LogDir         string            `json:"log_dir"`
	ISODir         string            `json:"iso_dir"`
	Theme          string            `json:"theme"` // dark, light
	Colors         map[string]string `json:"colors,omitempty"`
	Keybindings    map[string]string `json:"keybindings"`
	Editor         string            `json:"editor"`
}

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Dir returns the config directory.
func Dir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "qemu-tui")
	}
	return filepath.Join(home(), ".config", "qemu-tui")
}

// DataDir returns the data directory.
func DataDir() string {
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "qemu-tui")
	}
	return filepath.Join(home(), ".local", "share", "qemu-tui")
}

// Path returns the config file path.
func Path() string { return filepath.Join(Dir(), "config.json") }

// HistoryPath returns the console history file path.
func HistoryPath() string { return filepath.Join(DataDir(), "history") }

// DefaultKeybindings are the built-in key bindings.
func DefaultKeybindings() map[string]string {
	return map[string]string{
		"quit":    "ctrl+q",
		"new":     "ctrl+n",
		"refresh": "ctrl+r",
		"console": "ctrl+g",
		"search":  "/",
		"top":     "t",
	}
}

// Default returns the default configuration.
func Default() Config {
	// Only used for "edit raw config file"; everyday settings have an in-app screen.
	// Prefer beginner-friendly editors, then $VISUAL/$EDITOR, then vi.
	editor := ""
	for _, e := range []string{"micro", "nano", "ee", "pico"} {
		if _, err := exec.LookPath(e); err == nil {
			editor = e
			break
		}
	}
	if editor == "" {
		if editor = os.Getenv("VISUAL"); editor == "" {
			editor = os.Getenv("EDITOR")
		}
	}
	if editor == "" {
		editor = "vi"
	}
	if runtime.GOOS == "windows" {
		editor = "notepad"
	}
	d := DataDir()
	return Config{
		DefaultDisplay: "gtk",
		VMDir:          filepath.Join(d, "vms"),
		LogDir:         filepath.Join(d, "logs"),
		ISODir:         filepath.Join(d, "isos"),
		Theme:          "dark",
		Keybindings:    DefaultKeybindings(),
		Editor:         editor,
	}
}

func expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(home(), strings.TrimPrefix(p, "~"))
	}
	return p
}

// Load reads the config file; a missing file yields the defaults.
func Load() (Config, error) {
	c := Default()
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return Default(), err
	}
	c.VMDir, c.LogDir, c.ISODir = expand(c.VMDir), expand(c.LogDir), expand(c.ISODir)
	for k, v := range DefaultKeybindings() {
		if c.Keybindings == nil {
			c.Keybindings = map[string]string{}
		}
		if _, ok := c.Keybindings[k]; !ok {
			c.Keybindings[k] = v
		}
	}
	return c, nil
}

// Save writes the config file.
func (c Config) Save() error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(b, '\n'), 0o644)
}

// EnsureFile writes the default config if none exists, so Settings can open it.
func EnsureFile(c Config) error {
	if _, err := os.Stat(Path()); err == nil {
		return nil
	}
	return c.Save()
}
