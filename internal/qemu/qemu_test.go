package qemu

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

func base() Params {
	return Params{
		Name: "debian", Arch: "x86_64", MemoryMB: 4096, CPUs: 4, Accel: "tcg",
		DiskPath: "/vms/debian/disks/main.qcow2", DiskFormat: "qcow2",
		Firmware: "bios", NetMode: "user", MAC: "52:54:00:12:34:56",
		Display: "gtk", Boot: "disk", QMPNet: "unix", QMPAddr: "/run/q/debian.qmp",
	}
}

func has(args []string, pair ...string) bool {
	for i := 0; i+len(pair) <= len(args); i++ {
		ok := true
		for j, p := range pair {
			if args[i+j] != p {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestBuildBasic(t *testing.T) {
	bin, args, err := Build(base())
	if err != nil {
		t.Fatal(err)
	}
	if bin != "qemu-system-x86_64" {
		t.Fatalf("bin = %s", bin)
	}
	for _, want := range [][]string{
		{"-m", "4096"}, {"-smp", "4"}, {"-machine", "q35,accel=tcg"},
		{"-display", "gtk"}, {"-qmp", "unix:/run/q/debian.qmp,server=on,wait=off"},
		{"-boot", "order=cdn,menu=on"}, {"-serial", "none"},
	} {
		if !has(args, want...) {
			t.Errorf("missing %v in %v", want, args)
		}
	}
}

func TestBuildEscapesCommas(t *testing.T) {
	p := base()
	p.DiskPath = "/vms/a,b/main.qcow2"
	p.ISOPath = "/isos/x,y.iso"
	_, args, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "file=/vms/a,,b/main.qcow2,format=qcow2") {
		t.Error("disk path comma not escaped")
	}
	if !strings.Contains(joined, "file=/isos/x,,y.iso,media=cdrom") {
		t.Error("iso path comma not escaped")
	}
}

func TestBuildInjectionStaysOneArg(t *testing.T) {
	p := base()
	p.Name = "x; rm -rf /"
	_, args, _ := Build(p)
	if !has(args, "-name", "x; rm -rf /") {
		t.Error("name must be a single literal argument")
	}
}

func TestBuildVariants(t *testing.T) {
	p := base()
	p.Display, p.VNCDisplay = "vnc", 3
	_, a, _ := Build(p)
	if !has(a, "-vnc", "127.0.0.1:3") || !has(a, "-display", "none") {
		t.Errorf("vnc: %v", a)
	}
	p = base()
	p.NetMode = "none"
	_, a, _ = Build(p)
	if !has(a, "-nic", "none") {
		t.Errorf("no network: %v", a)
	}
	p = base()
	p.NetMode = "tap"
	if _, _, err := Build(p); err == nil {
		t.Error("tap without iface must fail")
	}
	p = base()
	p.Firmware = "uefi"
	if _, _, err := Build(p); err == nil {
		t.Error("uefi without firmware files must fail")
	}
	p.UEFICode, p.UEFIVars = "/c.fd", "/v.fd"
	_, a, err := Build(p)
	if err != nil || !has(a, "-drive", "if=pflash,format=raw,unit=0,readonly=on,file=/c.fd") {
		t.Errorf("uefi: %v %v", a, err)
	}
	p = base()
	p.Arch, p.Firmware = "aarch64", "uefi"
	p.UEFICode, p.UEFIVars = "/c.fd", "/v.fd"
	bin, a, _ := Build(p)
	if bin != "qemu-system-aarch64" || !has(a, "-machine", "virt,accel=tcg") || !has(a, "-device", "virtio-gpu-pci") {
		t.Errorf("aarch64: %s %v", bin, a)
	}
	p = base()
	p.USB = []USBDevice{{"046D", "C534"}}
	_, a, _ = Build(p)
	if !has(a, "-device", "usb-host,vendorid=0x046D,productid=0xC534,id=usb-046d-c534") {
		t.Errorf("usb: %v", a)
	}
	p = base()
	p.Arch = "sparc"
	if _, _, err := Build(p); err == nil {
		t.Error("unknown arch must fail")
	}
}

func TestQMPClient(t *testing.T) {
	srv, cli := net.Pipe()
	go func() {
		defer srv.Close()
		r := bufio.NewReader(srv)
		srv.Write([]byte(`{"QMP":{"version":{},"capabilities":[]}}` + "\n"))
		r.ReadString('\n') // qmp_capabilities
		srv.Write([]byte(`{"return":{}}` + "\n"))
		r.ReadString('\n') // query-status
		srv.Write([]byte(`{"event":"STOP"}` + "\n" + `{"return":{"status":"paused","running":false}}` + "\n"))
		r.ReadString('\n') // bad command
		srv.Write([]byte(`{"error":{"class":"CommandNotFound","desc":"nope"}}` + "\n"))
	}()
	c, err := NewClient(cli, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st, running, err := c.Status()
	if err != nil || st != "paused" || running {
		t.Fatalf("status %q %v %v", st, running, err)
	}
	if _, err := c.Execute("bogus", nil); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected error, got %v", err)
	}
}
