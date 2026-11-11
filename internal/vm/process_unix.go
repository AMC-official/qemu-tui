//go:build !windows

package vm

import (
	"os/exec"
	"syscall"
)

// detach puts QEMU in its own session so closing the TUI does not kill the VM.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func signalKill(pid int) error { return syscall.Kill(pid, syscall.SIGKILL) }
