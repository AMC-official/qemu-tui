// Command qemu-tui is a keyboard-first terminal UI for managing QEMU VMs.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/AMC-official/qemu-tui/internal/config"
	"github.com/AMC-official/qemu-tui/internal/console"
	"github.com/AMC-official/qemu-tui/internal/ui"
	"github.com/AMC-official/qemu-tui/internal/vm"
)

const version = "0.1.0"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("qemu-tui", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `qemu-tui - terminal UI for QEMU virtual machines

Usage:
  qemu-tui                  open the TUI
  qemu-tui --console        open the TUI directly in the global console
  qemu-tui <command> [...]  run one console command and exit (e.g. qemu-tui list)

Flags:`)
		fs.PrintDefaults()
	}
	openConsole := fs.Bool("console", false, "open the global command console")
	vmDir := fs.String("vm-dir", "", "VM directory (default from config)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "qemu-tui", version)
		return 0
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(stderr, "warning: %s: %v (using defaults)\n", config.Path(), err)
		cfg = config.Default()
	}
	if *vmDir != "" {
		cfg.VMDir = *vmDir
	}
	for _, d := range []string{cfg.VMDir, cfg.LogDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
	}
	mgr := vm.NewManager(cfg.VMDir, cfg.LogDir, cfg.ISODir)
	mgr.DefaultDisplay = cfg.DefaultDisplay

	if rest := fs.Args(); len(rest) > 0 {
		con := &console.Console{Mgr: mgr}
		out, err := con.ExecuteArgs(rest)
		if out != "" && err == nil {
			fmt.Fprintln(stdout, out)
		}
		if err != nil && !errors.Is(err, console.ErrExit) {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	if err := ui.Run(mgr, cfg, *openConsole); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
