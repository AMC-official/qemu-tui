package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AMC-official/qemu-tui/internal/qemu"
)

// runState is persisted in <vm>/runtime.json while a VM is running.
type runState struct {
	PID        int       `json:"pid"`
	QMPNet     string    `json:"qmp_net"`
	QMPAddr    string    `json:"qmp_addr"`
	SerialPath string    `json:"serial_path,omitempty"`
	Started    time.Time `json:"started"`
}

func readRunState(path string) (*runState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s runState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	if s.PID <= 0 || s.QMPAddr == "" {
		return nil, errors.New("corrupt runtime state")
	}
	return &s, nil
}

func writeRunState(path string, s runState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// processCmdline returns the command line of pid, space separated.
func processCmdline(pid int) (string, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return "", err
		}
		return strings.ReplaceAll(string(b), "\x00", " "), nil
	case "windows":
		return "", errors.New("command line lookup unsupported on windows")
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// verified reports whether the recorded PID is alive AND is really the QEMU we
// started for this VM (its command line contains our private QMP endpoint).
// This is what guarantees we never signal an unrelated process after PID reuse.
func (s *runState) verified() bool {
	if !pidAlive(s.PID) {
		return false
	}
	if runtime.GOOS == "windows" {
		c, err := qemu.Dial(s.QMPNet, s.QMPAddr, 500*time.Millisecond)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	cmd, err := processCmdline(s.PID)
	if err != nil {
		return false
	}
	return strings.Contains(cmd, "qemu") && strings.Contains(cmd, s.QMPAddr)
}

// kill force-terminates the VM process after re-verifying its identity.
func (s *runState) kill() error {
	if !s.verified() {
		return errors.New("refusing to kill: PID is not this VM's QEMU process")
	}
	return signalKill(s.PID)
}

func runtimeIsWindows() bool { return runtime.GOOS == "windows" }
