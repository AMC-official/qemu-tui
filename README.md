# QEMU-TUI 🖥️⚙️

A keyboard-first terminal UI for managing QEMU virtual machines, written in Go.
It generates QEMU command lines, launches QEMU directly, and controls it over QMP.
No daemon, no libvirt, one binary.

## Build

```sh
go mod tidy          # fetches Bubble Tea / Bubbles / Lipgloss and writes go.sum
go vet ./...
go test ./...
make build           # -> bin/qemu-tui
```

Needs `qemu-system-<arch>` and `qemu-img` in `PATH`. UEFI guests need OVMF/edk2
(common locations on Linux, FreeBSD and Homebrew are auto-detected).

## Use

```
qemu-tui                  open the TUI
qemu-tui --console        open the TUI directly in the global console
qemu-tui list             run a single console command and exit
```

### Main screen
| Key | Action |
|---|---|
| ↑ ↓ / j k | navigate |
| Enter | VM menu |
| `/` | search VMs (Enter keeps filter, Esc clears) |
| Ctrl+N | create VM (wizard) |
| `:` or Ctrl+G | global console |
| `t` | resource monitor (htop-like) |
| `e` | edit global config in your editor |
| Ctrl+R | refresh |
| Ctrl+Q | quit |

Keys are configurable under `keybindings` in the global config.

### VM menu
Start, Stop (ACPI), Force Stop, Restart (reset), Pause, Resume, Settings (opens the
VM's `config.json` in `$EDITOR` and re-validates it), Console, Logs, Snapshots,
Attach Media, Clone, Delete.

### Console commands
`:help :list :start :stop :restart :kill :pause :resume :status :top :create :delete
:clone :snapshot :restore :snapshots :logs :usb :disk :iso :boot :monitor :serial :exit`

- History (saved to `~/.local/share/qemu-tui/history`), Tab completion, PgUp/PgDn scroll.
- In a per-VM console the `<vm>` argument is implied: `:snapshot clean-install`,
  `:monitor info status`, `:usb attach 046d:c534`, `:list snapshots`.
- `:create mydebian mem=4096 cpus=4 disk=40G fw=uefi iso=debian.iso`
- `:disk info|resize|create|attach|detach`, `:iso list|attach|eject`

## Files

```
~/.config/qemu-tui/config.json        global config
~/.local/share/qemu-tui/vms/<name>/   config.json  disks/  firmware/  snapshots/  logs/
~/.local/share/qemu-tui/logs/         events.log (start/stop/exit status)
```

Global config keys: `default_display`, `vm_dir`, `log_dir`, `iso_dir`, `theme`
(`dark`/`light`), `colors` (accent, dim, ok, warn, err, text), `keybindings`, `editor`.

VM config example (extra optional keys: `disk_format`, `disk_size`, `disk_bus`,
`extra_disks`, `network_mode` user|tap|none, `tap_iface`, `mac`, `boot`, `serial`, `usb`):

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

## Safety model

- QEMU is started with `exec.Command(bin, args...)`; no shell is ever involved.
- VM names are `[A-Za-z0-9._-]` only; disk/ISO names must be plain file names; paths
  are joined with `SafeJoin` (no `..`, no absolute escapes); commas in paths are
  escaped for QEMU option strings.
- A VM's PID is only signalled after checking its command line contains that VM's
  private QMP endpoint, so a reused PID can never be killed. Stale runtime state is
  cleaned up automatically; duplicate starts are refused.
- QMP sockets live in a private `0700` runtime directory.

## Behaviour notes / limits

- Snapshots are internal qcow2 snapshots of the main disk and need the VM stopped.
- Disk attach/detach/resize need the VM stopped. USB and ISO changes apply live.
- Boot-order menu is applied on x86; other architectures follow firmware order.
- The per-VM console shows the QEMU log plus commands; use `:serial` for a guest
  serial port (enable `"serial": true`; connect with the `socat` line it prints).
- Network activity: link info comes from `info network`; byte counters are not
  exposed by QEMU without extra setup, so the monitor shows disk I/O and CPU/RAM.
- Windows uses a localhost TCP QMP port and is experimental.
- UI/CLI use Go's standard `flag` package instead of Cobra to keep dependencies small.
