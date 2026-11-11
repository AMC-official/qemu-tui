// Package system samples per-process resource usage.
package system

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stats is a resource snapshot for one process.
type Stats struct {
	CPUPercent float64 // 100 = one full core
	RSSBytes   uint64
	ReadBytes  uint64
	WriteBytes uint64
	ReadRate   float64 // bytes/s since the previous sample
	WriteRate  float64
}

type sample struct {
	at         time.Time
	cpuSeconds float64
	read       uint64
	write      uint64
}

// Sampler computes rates by comparing consecutive samples per PID.
type Sampler struct {
	mu   sync.Mutex
	prev map[int]sample
}

// NewSampler creates a Sampler.
func NewSampler() *Sampler { return &Sampler{prev: map[int]sample{}} }

const clockTicks = 100.0 // USER_HZ on Linux

// ParseProcStat extracts utime+stime (in ticks) from /proc/<pid>/stat content.
func ParseProcStat(s string) (float64, error) {
	i := strings.LastIndexByte(s, ')') // comm may contain spaces/parens
	if i < 0 {
		return 0, errors.New("malformed stat")
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0, errors.New("short stat")
	}
	ut, err1 := strconv.ParseFloat(f[11], 64)
	st, err2 := strconv.ParseFloat(f[12], 64)
	if err1 != nil || err2 != nil {
		return 0, errors.New("bad stat numbers")
	}
	return ut + st, nil
}

// ParseProcIO extracts read_bytes and write_bytes from /proc/<pid>/io content.
func ParseProcIO(s string) (read, write uint64) {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "read_bytes":
			read = n
		case "write_bytes":
			write = n
		}
	}
	return
}

func readLinux(pid int) (sample, uint64, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return sample{}, 0, err
	}
	ticks, err := ParseProcStat(string(b))
	if err != nil {
		return sample{}, 0, err
	}
	var rss uint64
	if m, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid)); err == nil {
		if f := strings.Fields(string(m)); len(f) > 1 {
			if pages, err := strconv.ParseUint(f[1], 10, 64); err == nil {
				rss = pages * uint64(os.Getpagesize())
			}
		}
	}
	sm := sample{at: time.Now(), cpuSeconds: ticks / clockTicks}
	if io, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid)); err == nil {
		sm.read, sm.write = ParseProcIO(string(io))
	}
	return sm, rss, nil
}

// ParsePS parses the output of `ps -o pcpu= -o rss=`. Only the last line is
// used, so a header that some ps implementations print anyway is ignored.
func ParsePS(out string) (cpu float64, rss uint64, err error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	f := strings.Fields(last)
	if len(f) < 2 {
		return 0, 0, fmt.Errorf("unexpected ps output %q", last)
	}
	cpu, err1 := strconv.ParseFloat(f[0], 64)
	kb, err2 := strconv.ParseUint(f[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("unexpected ps output %q", last)
	}
	return cpu, kb * 1024, nil
}

// readPS is the portable fallback (FreeBSD, macOS): decayed-average CPU, RSS.
func readPS(pid int) (float64, uint64, error) {
	out, err := exec.Command("ps", "-o", "pcpu=", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, 0, err
	}
	return ParsePS(string(out))
}

// Sample returns current stats for pid.
func (s *Sampler) Sample(pid int) (Stats, error) {
	if runtime.GOOS != "linux" {
		cpu, rss, err := readPS(pid)
		return Stats{CPUPercent: cpu, RSSBytes: rss}, err
	}
	cur, rss, err := readLinux(pid)
	if err != nil {
		return Stats{}, err
	}
	st := Stats{RSSBytes: rss, ReadBytes: cur.read, WriteBytes: cur.write}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.prev[pid]; ok {
		if dt := cur.at.Sub(p.at).Seconds(); dt > 0.2 {
			st.CPUPercent = (cur.cpuSeconds - p.cpuSeconds) / dt * 100
			if cur.read >= p.read {
				st.ReadRate = float64(cur.read-p.read) / dt
			}
			if cur.write >= p.write {
				st.WriteRate = float64(cur.write-p.write) / dt
			}
		} else {
			return st, nil // too soon; keep the old baseline
		}
	}
	s.prev[pid] = cur
	return st, nil
}

// Forget drops state for a pid.
func (s *Sampler) Forget(pid int) {
	s.mu.Lock()
	delete(s.prev, pid)
	s.mu.Unlock()
}

// HumanBytes formats a byte count.
func HumanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanDuration formats an uptime like 1h02m or 3d04h.
func HumanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}
