# QEMU-TUI 🖥️⚙️

A fast, keyboard-first terminal UI for creating and running QEMU virtual machines.
It is a thin management layer: QEMU-TUI builds a QEMU command line, starts QEMU
directly, and then controls it over QMP (the QEMU Machine Protocol).

- Written entirely in Go, compiled to a **single binary**
- **No daemon**, no libvirt, no background service; VMs keep running if you close the TUI
- Think *htop + lazygit + virt-manager*, but lightweight
- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
  [Bubbles](https://github.com/charmbracelet/bubbles) and
  [Lipgloss](https://github.com/charmbracelet/lipgloss); everything else is the Go standard library

> The terminal is not a limitation. The terminal is the interface.

> and another thing, AI is also **partially** used for debugging purposes

---

## Table of contents

1. [Features](#features)
2. [Requirements and installation](#requirements-and-installation)
3. [Quick start](#quick-start)
4. [Command line](#command-line)
5. [The TUI](#the-tui)
6. [Creating a VM](#creating-a-vm)
7. [The Settings screen](#the-settings-screen)
8. [Consoles and commands](#consoles-and-commands)
9. [Disks, snapshots, ISOs, USB, boot order](#disks-snapshots-isos-usb-boot-order)
10. [Networking and display backends](#networking-and-display-backends)
11. [Resource monitor](#resource-monitor)
12. [Logs](#logs)
13. [Files and configuration](#files-and-configuration)
14. [What QEMU command is generated](#what-qemu-command-is-generated)
15. [Process management and safety](#process-management-and-safety)
16. [Platform notes](#platform-notes)
17. [Project layout](#project-layout)
18. [Testing](#testing)
19. [Troubleshooting](#troubleshooting)
20. [Known limitations](#known-limitations)

---

## Features

| Area | What you get |
|---|---|
| VM list | State, CPU, RAM, disk size, network mode and uptime for every VM; live search |
| VM menu | Start, Stop, Force Stop, Restart, Pause, Resume, Settings, Console, Logs, Snapshots, Attach Media, Clone, Delete |
| Wizard | Step-by-step VM creation: name, architecture, RAM, CPUs, disk, firmware, network, display, ISO |
| Settings | Built-in settings screens for each VM and for the app itself (no hand-editing JSON) |
| Consoles | A global `:` console and a per-VM console with history, Tab completion and scrolling |
| Monitor | QMP access: send monitor commands (`info status`, `info block`, `info network`, ...) |
| Disks | Create qcow2 or raw disks, attach/detach extra disks, grow disks, show disk info (via `qemu-img`) |
| Snapshots | Internal qcow2 snapshots: create, list, restore, delete |
| Media | ISO browser, attach and eject (also while the VM runs), choose boot device |
| USB | Attach and remove host USB devices by `vendor:product` id (also live) |
| Resource view | htop-like CPU, RAM and disk I/O bars for running VMs |
| Safety | Never kills a process it did not start, no shell strings, validated paths, stale-PID cleanup |
| Look | Dark and light themes, custom colors, remappable keys, text re-wraps when you resize the terminal |

Supported guest architectures: `x86_64`, `aarch64`, `i386`, `riscv64`.

---

## Requirements and installation

**You need**

- Go 1.22 or newer (to build)
- `qemu-system-<arch>` for each architecture you want to run
- `qemu-img` (part of the QEMU packages)
- UEFI guests only: OVMF/edk2 firmware (see below)
- A terminal with UTF-8 support

Typical packages (names vary by distribution):

| System | Command |
|---|---|
| Debian/Ubuntu | `sudo apt install qemu-system-x86 qemu-utils ovmf` |
| Arch | `sudo pacman -S qemu-full edk2-ovmf` |
| Fedora | `sudo dnf install qemu-kvm edk2-ovmf` |
| FreeBSD | `sudo pkg install qemu edk2-qemu-x64` |
| macOS | `brew install qemu` |

UEFI firmware is auto-detected in the usual locations for Debian/Ubuntu, Fedora, Arch,
FreeBSD and Homebrew. If none is found, creating or starting a UEFI VM fails with a
clear message and BIOS VMs are unaffected.

**Build**

```sh
git clone <your repo> && cd qemu-tui
go mod tidy          # downloads Bubble Tea, Bubbles, Lipgloss; writes go.sum
go vet ./...
go test ./...
make build           # produces bin/qemu-tui
```

Or run it directly: `go run ./cmd/qemu-tui`. Copy `bin/qemu-tui` anywhere in your `PATH`.

---

## Quick start

```sh
mkdir -p ~/.local/share/qemu-tui/isos
cp ~/Downloads/alpine.iso ~/.local/share/qemu-tui/isos/
qemu-tui
```

1. Press **Ctrl+N** and answer the wizard (name, architecture, RAM, ...).
   For the ISO step, type the file name, for example `alpine.iso`.
2. Select the new VM and press **Enter**, then choose **Start**.
3. A QEMU window opens, and a console appears in the terminal showing the log.
4. After installing, set the boot device back to the disk if needed:
   VM menu, **Settings**, **Boot device**, or in a console: `:boot <vm> disk`.

The default boot order is *disk, then CD-ROM, then network*, so a new VM with an empty disk
falls through to the ISO automatically and boots from the disk after installation.

---

## Command line

```
qemu-tui                  open the TUI
qemu-tui --console        open the TUI directly in the global console
qemu-tui <command> [...]  run one console command and exit
qemu-tui --version
```

| Flag | Meaning |
|---|---|
| `--console` | Start in the global command console |
| `--vm-dir DIR` | Use a different VM directory for this run |
| `--version` | Print the version |

One-shot commands use the same command set as the console and are handy for scripts:

```sh
qemu-tui list
qemu-tui start debian
qemu-tui snapshot debian clean-install
qemu-tui monitor debian "info status"
```

Exit status is `0` on success and `1` on error (the message goes to stderr).

---

## The TUI

### Main screen

```
 QEMU-TUI
────────────────────────────────────────────────────────────────────────────
 ▸ debian               ● Running     4 CPU   4096 MB     20G  net:user  up 1h02m
   freebsd              ○ Stopped     2 CPU   2048 MB     20G  net:user  up -
   winxp                ❚❚ Paused     1 CPU    512 MB      8G  net:user  up 3h10m
```

On narrow terminals columns are dropped (uptime first, then disk and network, then CPU and
RAM) so rows never get cut off mid-word.

| Key | Action |
|---|---|
| `↑` `↓` / `j` `k` | Move the selection |
| `Enter` | Open the VM menu |
| `/` | Search VMs (type to filter, `Enter` keeps the filter, `Esc` clears it) |
| `Ctrl+N` | Create a new VM |
| `:` or `Ctrl+G` | Global console |
| `t` | Resource monitor |
| `s` (or `e`) | App settings |
| `Ctrl+R` | Refresh VM states (they also refresh every 2 seconds) |
| `Ctrl+Q` or `Ctrl+C` | Quit (VMs keep running) |

Key bindings for `quit`, `new`, `refresh`, `console`, `search` and `top` can be changed in
the global config (`keybindings`).

### VM menu

| Item | What it does |
|---|---|
| Start | Builds the QEMU command, launches QEMU, opens the VM console showing the log |
| Stop | Sends an ACPI power-down (graceful shutdown) |
| Force Stop | Tells QEMU to quit immediately; falls back to a verified kill of *its own* process |
| Restart | Resets the guest |
| Pause / Resume | Freezes or continues the guest |
| Settings | Settings screen for this VM |
| Console | Opens the per-VM console |
| Logs | Scrollable log viewer (`r` reloads) |
| Snapshots | List, create (`n`), restore (`Enter`/`r`), delete (`d`) |
| Attach Media | Prompts for an ISO name or path; an empty answer ejects |
| Clone | Copies a stopped VM (disk, firmware vars, snapshots) under a new name with a new MAC |
| Delete | Requires typing the VM name; removes the whole VM directory |

---

## Creating a VM

**Ctrl+N** (or `:create` with no arguments) opens the wizard:

| Step | Choices |
|---|---|
| Name | Letters, digits, `.` `_` `-` (max 63, must start with a letter or digit) |
| Architecture | `x86_64`, `aarch64`, `i386`, `riscv64` |
| RAM | MiB, minimum 16 (default 2048) |
| CPU cores | Default 2 |
| Disk size | For example `20G`, `512M`, `1.5T` |
| Disk format | `qcow2` or `raw` |
| Firmware | `bios` or `uefi` (non-x86 guests always use UEFI) |
| Network | `user`, `tap`, `none` |
| TAP interface | Only asked when Network is `tap` |
| Display | `gtk`, `sdl`, `vnc`, `spice` (default from your global settings) |
| ISO | File name in the ISO folder, or a full path; empty for none |

`Enter` moves on, `Shift+Tab` goes back, `←`/`→` changes a choice, `Esc` cancels.
Each answer is validated before you can continue.

You can also create a VM from any console:

```
:create debian mem=4096 cpus=4 disk=40G fw=uefi display=gtk iso=debian.iso
```

Keys: `mem` (or `memory`, `ram`), `cpus` (`cpu`), `arch`, `disk` (size), `fmt` (`format`),
`fw` (`firmware`), `net` (`network`), `tap`, `display`, `iso`, `boot`.

---

## The Settings screen

No hand-editing of JSON is needed for everyday changes.

- **VM settings:** VM menu, **Settings**
- **App settings:** `s` on the main screen

Move with `↑` `↓`. `←`/`→` changes choices and toggles and saves immediately. `Enter` edits
text and number fields (`Enter` saves, `Esc` cancels). Invalid values show an error and are
**not** saved.

**VM settings**

| Setting | Notes |
|---|---|
| Architecture, Disk | Read-only information |
| Memory (MiB), CPU cores | Numbers |
| Display | gtk, sdl, vnc, spice |
| Boot device | disk, cdrom, network |
| Firmware | bios or uefi |
| Network | user, tap, none. Choosing `tap` fills in `tap0` if no interface is set |
| TAP interface | Only used with `tap` |
| Disk bus | `virtio` (default) or `ide` for old guests such as Windows XP or DOS |
| Serial console | Adds a serial port reachable via `:serial` |
| MAC address | `52:54:00:xx:xx:xx` style |
| ISO | Name or path; empty ejects. Applied live if the VM is running |
| Edit raw config file... | Advanced: opens `config.json` in your editor |

Changes apply on the **next start** (the screen tells you if the VM is running), except ISO
changes, which are swapped live.

**App settings**

| Setting | Notes |
|---|---|
| Default display | Used for newly created VMs |
| Theme | `dark` or `light`, applied immediately |
| ISO folder | Where Attach Media and the wizard look for `.iso` files (`~` is expanded, folder is created) |
| Raw file editor | Command used by "Edit raw config file..." |
| VM folder, Log folder, Config file | Read-only; change them in the raw file |
| Edit raw config file... | Key bindings, custom colors and folders live here |

The "raw editor" default prefers beginner-friendly editors (`micro`, `nano`, `ee`, `pico`),
then `$VISUAL` / `$EDITOR`, then `vi`. It is only used for the raw-file rows.

---

## Consoles and commands

Open the **global console** with `:`, `Ctrl+G` or `qemu-tui --console`. Open a
**per-VM console** from the VM menu (or by starting a VM). In a per-VM console the `<vm>`
argument is implied, so `:snapshot clean-install` snapshots *that* VM.

Console keys: `Enter` run, `Tab` complete, `↑`/`↓` history, `PgUp`/`PgDn` scroll, `Esc` close.
History is saved to `~/.local/share/qemu-tui/history` (last 500 commands).
Output wraps to the terminal width, also after you resize.

| Command | Description |
|---|---|
| `:help` | List all commands |
| `:list` | List VMs. `:list snapshots` lists snapshots of a VM |
| `:status [vm]` | Overview of all VMs, or details: PID, runtime, CPU, memory, disk I/O, network |
| `:top` | Open the live resource monitor |
| `:start <vm>` | Start a VM |
| `:stop <vm>` | ACPI power-down |
| `:restart <vm>` | Reset the guest |
| `:kill <vm>` | Force stop |
| `:pause <vm>` / `:resume <vm>` | Freeze / continue |
| `:create <name> [key=value ...]` | Create a VM (no arguments opens the wizard) |
| `:delete <vm>` | Delete a stopped VM and all its files |
| `:clone <vm> <new>` | Clone a stopped VM |
| `:snapshot <vm> <name>` | Create a snapshot (VM must be stopped) |
| `:restore <vm> <name>` | Restore a snapshot (VM must be stopped) |
| `:snapshots <vm>` | List snapshots |
| `:logs [vm]` | Recent log lines; without a VM, the global event log |
| `:usb attach <vm> <vvvv:pppp>` | Attach a host USB device (get ids from `lsusb`) |
| `:usb remove <vm> <vvvv:pppp>` | Detach it |
| `:disk info <vm>` | Show virtual size, on-disk size and snapshot count of each disk |
| `:disk resize <vm> <size\|+size>` | Grow the main disk (VM stopped) |
| `:disk create <vm> <file> <size> [qcow2\|raw]` | Create and attach an extra disk |
| `:disk attach <vm> <file>` / `:disk detach <vm> <file>` | Attach an existing file from `disks/` / detach it (file is kept) |
| `:iso list` | ISOs in the ISO folder |
| `:iso attach <vm> <file>` / `:iso eject <vm>` | Insert / eject media |
| `:boot <vm> disk\|cdrom\|network` | Choose the boot device (next start) |
| `:monitor <vm> <command>` | Run a QEMU monitor command, for example `:monitor debian info block` |
| `:serial [vm]` | Show how to connect to the serial console |
| `:clear` | Clear the console screen (TUI only) |
| `:exit` / `:quit` | Close the console |

Arguments are split with simple shell-like quoting (`"two words"`, `'single'`, `\ `), but
**never run through a shell**.

**Serial console:** enable *Serial console* in the VM settings, start the VM, then run
`:serial`. It prints a ready-to-paste command such as
`socat -,raw,echo=0,escape=0x1d UNIX-CONNECT:/run/user/1000/qemu-tui/debian.serial`
(press `Ctrl+]` to leave).

---

## Disks, snapshots, ISOs, USB, boot order

**Disks.** Created with `qemu-img create`. Sizes look like `20G`, `512M`, `1.5T`. Disk files
live in the VM's `disks/` folder, and only plain file names are accepted. Resizing only
grows (`40G` or `+10G`); grow the partition inside the guest afterwards. Attaching,
detaching and resizing need the VM stopped.

**Snapshots.** Internal qcow2 snapshots of the main disk (`qemu-img snapshot -c/-a/-d`).
They need the VM stopped (QEMU locks the image while it runs) and a qcow2 disk; raw disks are
refused with an explanation. Names: letters, digits, `.` `_` `-`.
Listing shows the snapshot name and creation date.

**ISOs.** Name an ISO from the ISO folder (`~/.local/share/qemu-tui/isos` by default) or
give a full path. Lookup order for relative names: ISO folder, then the VM directory.
Attach and eject also work **while the VM runs**. Every VM always has a CD-ROM drive so
media can be inserted later without restarting.

**USB.** `:usb attach <vm> 046d:c534` adds the device to the VM's config and, if the VM is
running, hot-plugs it via QMP. The host user needs permission to access the USB device.

**Boot device.** `disk` (disk, CD, network), `cdrom` (CD, disk, network), `network`
(network, CD, disk). The boot menu is applied on x86 guests; other architectures follow
their firmware's own order.

---

## Networking and display backends

**Network modes**

| Mode | Behaviour |
|---|---|
| `user` | QEMU user-mode NAT. No setup or privileges; guest can reach out, host cannot reach in by default |
| `tap` | Uses an existing host tap interface (`script=no`). You create and configure the interface yourself |
| `none` | No network |

The NIC is `virtio-net-pci` with the VM's MAC address (generated as `52:54:00:xx:xx:xx`;
clones get a fresh one). Old guests without virtio drivers (Windows XP, DOS) need a
different NIC model, which QEMU-TUI does not offer yet.

**Display backends**

| Backend | Behaviour |
|---|---|
| `gtk`, `sdl` | A normal QEMU window (needs a QEMU build with that display) |
| `vnc` | No window; listens on `127.0.0.1:5900+n` (first free port; logged at start) |
| `spice` | No window; listens on `127.0.0.1` at the first free port from 5930 |

VNC and SPICE listen on localhost only, without a password.

---

## Resource monitor

Press `t` (or `:top`). It refreshes every second:

```
 Resource monitor - 2 running
 debian  ● Running
   CPU: ███████████████░░░░░░░░░  72%
   RAM: ██████████░░░░░░░░░░░░░░  40%  1.6 GiB
   Disk IO: ↓ 3.2 MiB/s  ↑ 120.0 KiB/s   Net: user
```

CPU is shown relative to the VM's core count. RAM is the QEMU process's resident memory
compared to the configured memory. See [Platform notes](#platform-notes) for what each OS
can measure.

---

## Logs

- **Per VM:** `<vm>/logs/qemu.log` contains QEMU's own output and errors plus timestamped
  events: *Starting QEMU..., Using KVM acceleration, VNC listening..., Boot sequence
  started (pid N), QEMU exited (status N), ACPI power-down requested, Force stopped,
  Snapshot created...*.
- **Global:** `~/.local/share/qemu-tui/logs/events.log` has the same events for all VMs,
  with date, time and VM name (start time, stop time, exit status, cleanups).

View them in the Logs screen, or with `:logs [vm]`. If a VM dies right after starting, the
error message includes the last lines of the log.

---

## Files and configuration

```
~/.config/qemu-tui/config.json            global config (honors $XDG_CONFIG_HOME)
~/.local/share/qemu-tui/                  honors $XDG_DATA_HOME
    vms/<name>/
        config.json                       VM definition
        runtime.json                      exists only while the VM runs (PID, QMP endpoint)
        disks/       main.qcow2 ...
        firmware/    vars.fd              per-VM UEFI variable store
        snapshots/                        reserved (snapshots are stored inside the qcow2)
        logs/        qemu.log
    isos/                                 default ISO folder
    logs/            events.log
    history                               console history
```

QMP and serial sockets live in `$XDG_RUNTIME_DIR/qemu-tui/` (or `/tmp/qemu-tui-<uid>/`),
a directory with mode `0700`.

### Global config

```json
{
    "default_display": "gtk",
    "vm_dir": "~/.local/share/qemu-tui/vms",
    "log_dir": "~/.local/share/qemu-tui/logs",
    "iso_dir": "~/.local/share/qemu-tui/isos",
    "theme": "dark",
    "colors": { "accent": "39", "dim": "244", "ok": "42", "warn": "214", "err": "196", "text": "252" },
    "keybindings": {
        "quit": "ctrl+q", "new": "ctrl+n", "refresh": "ctrl+r",
        "console": "ctrl+g", "search": "/", "top": "t"
    },
    "editor": "nano"
}
```

Missing keys fall back to defaults. Colors are ANSI numbers (`"39"`) or hex (`"#00afff"`);
`colors` overrides either theme. `~` is expanded in folder paths.

### VM config

Only `name` is required; everything else has a default.

```json
{
    "name": "debian",
    "memory": 4096,
    "cpus": 4,
    "architecture": "x86_64",
    "disk": "main.qcow2",
    "iso": "debian.iso",
    "firmware": "uefi",
    "network": true,
    "display": "gtk"
}
```

| Key | Values | Default |
|---|---|---|
| `name` | `[A-Za-z0-9._-]`, must match the folder name | required |
| `memory` | MiB, 16 to 4194304 | 2048 |
| `cpus` | 1 to 512 | 2 |
| `architecture` | `x86_64` `aarch64` `i386` `riscv64` | `x86_64` |
| `disk` | file name inside `disks/` | `main.qcow2` (or `main.raw`) |
| `disk_format` | `qcow2` `raw` | `qcow2` (`raw` for `.raw`/`.img` names) |
| `disk_size` | `20G`-style, informational | |
| `disk_bus` | `virtio`, `ide` (x86 only) | `virtio` |
| `extra_disks` | list of file names in `disks/` | none |
| `iso` | file name (ISO folder, then VM folder) or absolute path | none |
| `firmware` | `bios` (x86 only), `uefi` | `bios` on x86, `uefi` otherwise |
| `network` | boolean shortcut: true means `user`, false means `none` | |
| `network_mode` | `user` `tap` `none` (overrides `network`) | `none` unless `network` is true |
| `tap_iface` | interface name (letters, digits, `_ . -`, max 15) | |
| `mac` | `aa:bb:cc:dd:ee:ff` | generated at creation |
| `display` | `gtk` `sdl` `vnc` `spice` | global `default_display` |
| `boot` | `disk` `cdrom` `network` | `disk` |
| `serial` | boolean | false |
| `usb` | list of `"vvvv:pppp"` | none |

A broken or invalid VM config is skipped in the list; fix it from the Settings screen's raw
editor row or any editor. Opening Settings → raw config re-validates the file when you return.

---

## What QEMU command is generated

QEMU-TUI picks sensible defaults so you rarely have to think about flags:

- **Binary:** `qemu-system-<architecture>`
- **Machine:** `q35` on x86, `virt` on ARM/RISC-V
- **Accelerator (auto):** `kvm` on Linux with `/dev/kvm` and a native guest, `hvf` on macOS,
  `whpx` on Windows, otherwise `tcg` (software emulation; always used for non-native guests
  and on FreeBSD)
- **CPU:** `host` with KVM/HVF, `max` otherwise
- **Disk:** `virtio` (or `ide` if selected), plus extra disks
- **CD-ROM:** always present with id `cd0` (IDE on x86, virtio-scsi elsewhere)
- **UEFI:** two pflash drives (read-only code plus your VM's own `vars.fd`)
- **Graphics/input:** `virtio-gpu-pci` on ARM/RISC-V, an xHCI USB controller, a USB tablet
  (and keyboard on non-x86) for smooth mouse handling
- **Control:** `-qmp` on a private socket, and `-serial none` unless the serial console is on

Paths containing commas are escaped (`,,`) so they cannot inject extra QEMU options.

---

## Process management and safety

- QEMU is started with `exec.Command(binary, args...)`. **No shell is ever used**, and VM
  config values never reach a shell.
- QEMU runs in its own session, so quitting the TUI does **not** stop your VMs.
- The PID is stored in `runtime.json` only while the VM runs.
- Before QEMU-TUI signals a PID it checks that the process command line contains **that VM's
  private QMP socket**. A recycled PID or a foreign process is never touched; the stored state
  is simply treated as stale and cleaned up.
- Stale state (crashed VM, reboot of the host, corrupted file) is cleaned up automatically
  and logged. Duplicate starts of the same VM are refused.
- Graceful stop uses ACPI; Force Stop uses QMP `quit` and only then a verified kill.
- Validation everywhere: VM names, disk and ISO file names must be plain names; paths are
  joined with a traversal-safe `SafeJoin`; MAC, interface, USB ids, sizes and snapshot names
  are checked against strict patterns; delete refuses directories without a `config.json`
  and symlinked VM directories; clone refuses symlinks.
- The runtime directory is created with mode `0700`; QMP is never exposed on the network
  (Windows uses `127.0.0.1` only).

---

## Platform notes

| | Linux | FreeBSD | macOS | Windows |
|---|---|---|---|---|
| Status | Primary | Primary | Expected | Experimental |
| Acceleration | KVM | TCG only | HVF | WHPX |
| CPU and RAM numbers | from `/proc` | from `ps` | from `ps` | not tested |
| Disk I/O numbers | yes | not available (shows 0) | not available (shows 0) | not available |
| QMP transport | Unix socket | Unix socket | Unix socket | localhost TCP |

Notes:

- **FreeBSD** has no KVM, so guests run under TCG and are noticeably slower; expect high host
  CPU while a guest is busy booting. CPU% comes from `ps` and is a decayed average, so it
  reacts slowly.
- Cross-architecture guests (for example `aarch64` on an x86 host) always use TCG.
- On Windows, process identity checks cannot read command lines, so QEMU-TUI verifies a VM
  by connecting to its QMP port instead.

---

## Project layout

```
cmd/qemu-tui/main.go          flags, wiring, one-shot commands
internal/
    config/config.go          global config: load, save, paths, defaults
    vm/
        config.go             VM config, validation, SafeJoin, MAC generation
        manager.go            create, clone, delete, start, stop, QMP actions, ISO, USB, disks, snapshots, logs
        process.go            runtime state, identity verification
        process_unix.go       detach (new session), liveness, kill
        process_windows.go    Windows equivalents
    qemu/
        command.go            QEMU command-line builder, accelerator and UEFI detection
        monitor.go            QMP client
    storage/
        disks.go              qemu-img create/info/resize
        snapshots.go          qcow2 snapshot operations
    console/
        commands.go           command parser, all console commands, completion
        history.go            persistent history
    system/stats.go           per-process CPU, memory, I/O sampling
    ui/
        screens.go            Bubble Tea model: list, menu, console, wizard, logs, top, snapshots
        settings.go           in-app Settings screens
        widgets.go            themes, wrapping, footer, helpers
```

The UI never talks to QEMU directly; it calls `vm.Manager`, and the console is plain Go that
the UI, the one-shot CLI and the tests all share.

---

## Testing

```sh
go test ./...
go vet ./...
```

| Package | Covered |
|---|---|
| `vm` | Config parsing (including the spec example), validation, path traversal, stale/foreign PID handling, delete safety, ISO and USB handling, create/clone/delete (needs `qemu-img`) |
| `qemu` | Command generation (variants, comma escaping, injection stays one argument), QMP client against a fake server |
| `storage` | `qemu-img info` parsing, validation, snapshot round-trip (needs `qemu-img`) |
| `console` | Tokenizer, commands, per-VM scope, completion, history |
| `system` | `/proc` and `ps` parsing, formatting |
| `ui` | Text wrapping, settings behavior, every screen fits a 40-column terminal |

Tests that need `qemu-img` skip themselves if it is not installed.

---

## Troubleshooting

| Problem | What to do |
|---|---|
| `qemu-system-x86_64 not found in PATH` / `qemu-img not found` | Install QEMU (see [Requirements](#requirements-and-installation)) |
| `no UEFI firmware found` | Install OVMF/edk2 for your system, or choose `bios` for x86 guests |
| QEMU exits immediately | The console shows the last log lines; also check `<vm>/logs/qemu.log` |
| `display gtk/sdl not available` | Your QEMU build lacks that display: install the full QEMU package or switch to `vnc` |
| VM is very slow | Without KVM/HVF/WHPX QEMU uses software emulation. On Linux, make sure `/dev/kvm` exists and your user may use it |
| Network `tap` fails | The tap interface must already exist and be usable by your user |
| USB attach fails | Check the id with `lsusb` and your permissions on the device |
| `Ctrl+Q` does nothing | Some terminals use it for flow control; use `Ctrl+C`, or change `keybindings.quit` |
| Config problem after editing the raw file | The status line shows the validation error; fix the value or use the Settings screen |
| Colors look wrong | Try `"theme": "light"` or override `colors` in the config |

---

## Known limitations

- The per-VM console shows the QEMU log and accepts QEMU-TUI/QMP commands. It is **not** a
  terminal into the guest; use the VM window, VNC/SPICE, or the serial console for that.
- No network byte counters: QEMU does not expose them without extra setup, so the monitor
  shows link information (`info network`) and CPU, RAM and disk I/O only.
- Disk I/O numbers exist on Linux only; CPU and RAM on other systems come from `ps`.
- Snapshots are offline, internal, main-disk only. Live (`savevm`) snapshots are not
  supported yet.
- Adding, removing and growing disks need the VM stopped (no disk hot-plug).
- The NIC model is fixed to `virtio-net-pci`.
- Widths are measured in characters, so very wide characters (CJK, some emoji) may misalign
  columns.
- Windows support is experimental and untested.
- The UI and CLI use Go's standard `flag` package instead of Cobra to keep the dependency
  list to three libraries.

Ideas for later: selectable NIC model, live snapshots, FreeBSD disk I/O numbers, per-VM
extra QEMU arguments with validation, a bridge/tap helper, VNC/SPICE connect shortcuts.
