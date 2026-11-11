package system

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestParseProcStat(t *testing.T) {
	// comm contains spaces and a parenthesis on purpose
	s := "1234 (qemu (x) sys) S 1 1 1 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 5 0 100 1000 200"
	ticks, err := ParseProcStat(s)
	if err != nil || ticks != 300 {
		t.Fatalf("ticks=%v err=%v", ticks, err)
	}
	if _, err := ParseProcStat("garbage"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseProcIO(t *testing.T) {
	r, w := ParseProcIO("rchar: 5\nread_bytes: 4096\nwrite_bytes: 8192\n")
	if r != 4096 || w != 8192 {
		t.Fatalf("r=%d w=%d", r, w)
	}
}

func TestSampleSelf(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	s := NewSampler()
	st, err := s.Sample(os.Getpid())
	if err != nil || st.RSSBytes == 0 {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestFormatting(t *testing.T) {
	if HumanBytes(1536) != "1.5 KiB" || HumanBytes(10) != "10 B" {
		t.Error("HumanBytes")
	}
	if HumanDuration(3725*time.Second) != "1h02m" {
		t.Error("HumanDuration")
	}
}

func TestParsePS(t *testing.T) {
	cases := map[string]struct {
		cpu float64
		rss uint64
	}{
		" 96.5 1253988\n":            {96.5, 1253988 * 1024},
		"%CPU     RSS\n 3.0  2048\n": {3.0, 2048 * 1024}, // header printed anyway
	}
	for in, want := range cases {
		cpu, rss, err := ParsePS(in)
		if err != nil || cpu != want.cpu || rss != want.rss {
			t.Errorf("ParsePS(%q) = %v %v %v", in, cpu, rss, err)
		}
	}
	for _, bad := range []string{"", "%CPU RSS\n", "abc def\n"} {
		if _, _, err := ParsePS(bad); err == nil {
			t.Errorf("ParsePS(%q) should fail", bad)
		}
	}
}
