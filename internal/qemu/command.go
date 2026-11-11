// Package qemu builds QEMU command lines and talks to the QEMU monitor.
package qemu

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// USBDevice is a host USB device identified by vendor/product id.
type USBDevice struct {
	VendorID  string
	ProductID string
}

// ID returns the stable QEMU device id.
func (u USBDevice) ID() string {
	return "usb-" + strings.ToLower(u.VendorID) + "-" + strings.ToLower(u.ProductID)
}

// ExtraDisk is an additional attached disk.
type ExtraDisk struct {
	Path   string
	Format string
}

// Params is everything needed to build a QEMU command line.
type Params struct {
	Name     string
	Arch     string
	MemoryMB int
	CPUs     int
	Accel    string // kvm, hvf, whpx, tcg

	DiskPath   string
	DiskFormat string
	DiskBus    string // virtio (default) or ide (x86 only)
	ExtraDisks []ExtraDisk
	ISOPath    string

	Firmware string // bios or uefi
	UEFICode string
	UEFIVars string

	NetMode  string // user, tap, none
	TapIface string
	MAC      string

	Display    string // gtk, sdl, vnc, spice
	VNCDisplay int
	SpicePort  int

	Boot string // disk, cdrom, network

	QMPNet     string // unix or tcp
	QMPAddr    string
	SerialPath string // unix socket path; empty disables the serial port
	USB        []USBDevice
}

// esc escapes a value for use inside a comma-separated QEMU option.
func esc(s string) string { return strings.ReplaceAll(s, ",", ",,") }

// IsX86 reports whether arch is an x86 architecture.
func IsX86(arch string) bool { return arch == "x86_64" || arch == "i386" }

// Binary returns the qemu-system binary name for an architecture.
func Binary(arch string) (string, error) {
	switch arch {
	case "x86_64", "i386", "aarch64", "riscv64":
		return "qemu-system-" + arch, nil
	}
	return "", fmt.Errorf("unsupported architecture %q", arch)
}

// Build returns the binary name and argument vector. Nothing is run through a shell.
func Build(p Params) (string, []string, error) {
	bin, err := Binary(p.Arch)
	if err != nil {
		return "", nil, err
	}
	if p.Name == "" || p.DiskPath == "" || p.MemoryMB <= 0 || p.CPUs <= 0 {
		return "", nil, errors.New("incomplete VM parameters")
	}
	x86 := IsX86(p.Arch)

	machine := "virt"
	if x86 {
		machine = "q35"
	}
	if p.Accel != "" {
		machine += ",accel=" + p.Accel
	}
	a := []string{"-name", p.Name, "-machine", machine}
	switch {
	case p.Accel == "kvm" || p.Accel == "hvf":
		a = append(a, "-cpu", "host")
	case p.Arch != "riscv64":
		a = append(a, "-cpu", "max")
	}
	a = append(a, "-m", fmt.Sprint(p.MemoryMB), "-smp", fmt.Sprint(p.CPUs))

	// Firmware
	if p.Firmware == "uefi" {
		if p.UEFICode == "" || p.UEFIVars == "" {
			return "", nil, errors.New("UEFI firmware files not found")
		}
		a = append(a,
			"-drive", "if=pflash,format=raw,unit=0,readonly=on,file="+esc(p.UEFICode),
			"-drive", "if=pflash,format=raw,unit=1,file="+esc(p.UEFIVars))
	}

	// Main disk
	bus := "virtio"
	if x86 && p.DiskBus == "ide" {
		bus = "ide"
	}
	disk := "file=" + esc(p.DiskPath) + ",format=" + p.DiskFormat + ",if=" + bus
	if bus == "ide" {
		disk += ",index=0"
	}
	a = append(a, "-drive", disk)
	for _, d := range p.ExtraDisks {
		a = append(a, "-drive", "file="+esc(d.Path)+",format="+d.Format+",if=virtio")
	}

	// CD-ROM (always present so media can be swapped at runtime; id is cd0)
	if x86 {
		cd := "media=cdrom,readonly=on,if=ide,index=2,id=cd0"
		if p.ISOPath != "" {
			cd = "file=" + esc(p.ISOPath) + "," + cd
		}
		a = append(a, "-drive", cd)
	} else {
		cd := "if=none,id=cdrive0,media=cdrom,readonly=on"
		if p.ISOPath != "" {
			cd = "file=" + esc(p.ISOPath) + "," + cd
		}
		a = append(a, "-device", "virtio-scsi-pci,id=scsi0", "-drive", cd,
			"-device", "scsi-cd,drive=cdrive0,bus=scsi0.0,id=cd0")
	}

	// Boot order (x86 firmware menu); other archs follow the firmware's order.
	if x86 {
		order := map[string]string{"disk": "cdn", "cdrom": "dcn", "network": "ncd"}[p.Boot]
		if order == "" {
			order = "cdn"
		}
		a = append(a, "-boot", "order="+order+",menu=on")
	}

	// Network
	switch p.NetMode {
	case "user", "tap":
		nd := "user,id=net0"
		if p.NetMode == "tap" {
			if p.TapIface == "" {
				return "", nil, errors.New("tap networking needs an interface name")
			}
			nd = "tap,id=net0,ifname=" + esc(p.TapIface) + ",script=no,downscript=no"
		}
		dev := "virtio-net-pci,netdev=net0"
		if p.MAC != "" {
			dev += ",mac=" + p.MAC
		}
		a = append(a, "-netdev", nd, "-device", dev)
	default:
		a = append(a, "-nic", "none")
	}

	// Display
	switch p.Display {
	case "gtk", "sdl":
		a = append(a, "-display", p.Display)
	case "vnc":
		a = append(a, "-display", "none", "-vnc", fmt.Sprintf("127.0.0.1:%d", p.VNCDisplay))
	case "spice":
		a = append(a, "-display", "none", "-spice",
			fmt.Sprintf("addr=127.0.0.1,port=%d,disable-ticketing=on", p.SpicePort))
	default:
		return "", nil, fmt.Errorf("unsupported display %q", p.Display)
	}
	if !x86 {
		a = append(a, "-device", "virtio-gpu-pci")
	}

	// USB
	a = append(a, "-device", "qemu-xhci,id=xhci", "-device", "usb-tablet")
	if !x86 {
		a = append(a, "-device", "usb-kbd")
	}
	for _, u := range p.USB {
		a = append(a, "-device", fmt.Sprintf("usb-host,vendorid=0x%s,productid=0x%s,id=%s",
			u.VendorID, u.ProductID, u.ID()))
	}

	// Control + serial
	switch p.QMPNet {
	case "unix":
		a = append(a, "-qmp", "unix:"+esc(p.QMPAddr)+",server=on,wait=off")
	case "tcp":
		a = append(a, "-qmp", "tcp:"+p.QMPAddr+",server=on,wait=off")
	default:
		return "", nil, errors.New("missing QMP endpoint")
	}
	if p.SerialPath != "" {
		a = append(a, "-serial", "unix:"+esc(p.SerialPath)+",server=on,wait=off")
	} else {
		a = append(a, "-serial", "none")
	}
	return bin, a, nil
}

// DetectAccel picks the best accelerator for the guest architecture.
func DetectAccel(arch string) string {
	native := (arch == "x86_64" && runtime.GOARCH == "amd64") ||
		(arch == "i386" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "386")) ||
		(arch == "aarch64" && runtime.GOARCH == "arm64") ||
		(arch == "riscv64" && runtime.GOARCH == "riscv64")
	if !native {
		return "tcg"
	}
	switch runtime.GOOS {
	case "linux":
		if f, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0); err == nil {
			f.Close()
			return "kvm"
		}
	case "darwin":
		return "hvf"
	case "windows":
		return "whpx"
	}
	return "tcg"
}

type fwPair struct{ code, vars string }

var uefiCandidates = map[string][]fwPair{
	"x86_64": {
		{"/usr/share/OVMF/OVMF_CODE_4M.fd", "/usr/share/OVMF/OVMF_VARS_4M.fd"},
		{"/usr/share/OVMF/OVMF_CODE.fd", "/usr/share/OVMF/OVMF_VARS.fd"},
		{"/usr/share/edk2/ovmf/OVMF_CODE.fd", "/usr/share/edk2/ovmf/OVMF_VARS.fd"},
		{"/usr/share/edk2/x64/OVMF_CODE.4m.fd", "/usr/share/edk2/x64/OVMF_VARS.4m.fd"},
		{"/usr/share/edk2-ovmf/x64/OVMF_CODE.fd", "/usr/share/edk2-ovmf/x64/OVMF_VARS.fd"},
		{"/usr/local/share/edk2-qemu/QEMU_UEFI_CODE-x86_64.fd", "/usr/local/share/edk2-qemu/QEMU_UEFI_VARS-x86_64.fd"},
		{"/opt/homebrew/share/qemu/edk2-x86_64-code.fd", "/opt/homebrew/share/qemu/edk2-i386-vars.fd"},
	},
	"aarch64": {
		{"/usr/share/AAVMF/AAVMF_CODE.fd", "/usr/share/AAVMF/AAVMF_VARS.fd"},
		{"/usr/share/edk2/aarch64/QEMU_EFI-pflash.raw", "/usr/share/edk2/aarch64/vars-template-pflash.raw"},
		{"/usr/local/share/edk2-qemu/QEMU_UEFI_CODE-aarch64.fd", "/usr/local/share/edk2-qemu/QEMU_UEFI_VARS-aarch64.fd"},
		{"/opt/homebrew/share/qemu/edk2-aarch64-code.fd", "/opt/homebrew/share/qemu/edk2-arm-vars.fd"},
	},
	"riscv64": {
		{"/usr/share/qemu-efi-riscv64/RISCV_VIRT_CODE.fd", "/usr/share/qemu-efi-riscv64/RISCV_VIRT_VARS.fd"},
	},
}

// FindUEFI locates a UEFI code image and a vars template for the architecture.
func FindUEFI(arch string) (code, vars string, err error) {
	if arch == "i386" {
		return "", "", errors.New("UEFI is not supported for i386; use BIOS")
	}
	for _, c := range uefiCandidates[arch] {
		if exists(c.code) && exists(c.vars) {
			return c.code, c.vars, nil
		}
	}
	return "", "", fmt.Errorf("no UEFI firmware found for %s (install OVMF/edk2)", arch)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// LookPath finds the qemu binary in PATH.
func LookPath(bin string) (string, error) {
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", bin)
	}
	return p, nil
}
