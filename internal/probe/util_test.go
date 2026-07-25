package probe

import (
	"testing"
	"time"
)

func TestHumanBytesPerSec(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0 B/s"},
		{512, "512 B/s"},
		{2048, "2.0 KiB/s"},
		{5 * 1024 * 1024, "5.0 MiB/s"},
	}
	for _, c := range cases {
		if got := HumanBytesPerSec(c.in); got != c.want {
			t.Errorf("HumanBytesPerSec(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestClampHistory(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5}

	got := ClampHistory(s, 3)
	if len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Errorf("ClampHistory keep-tail: %v", got)
	}

	got = ClampHistory(s, 10)
	if len(got) != 5 {
		t.Errorf("under cap should be unchanged: %v", got)
	}

	got = ClampHistory(s, 0)
	if len(got) != 0 {
		t.Errorf("cap 0 should empty the slice: %v", got)
	}

	ints := ClampHistory([]int{1, 2, 3}, 2) // generic over element type
	if len(ints) != 2 || ints[0] != 2 {
		t.Errorf("int history: %v", ints)
	}
}

func TestSince(t *testing.T) {
	if d := Since(time.Time{}); d != 0 {
		t.Errorf("Since(zero) = %v", d)
	}
	if d := Since(time.Now().Add(-time.Hour)); d < 59*time.Minute {
		t.Errorf("Since(1h ago) = %v", d)
	}
}

func TestProcFormatters(t *testing.T) {
	if got := FormatProcCPU(12.34); got != "12.3%" {
		t.Errorf("FormatProcCPU = %q", got)
	}
	if got := FormatProcMem(5.67); got != "5.7%" {
		t.Errorf("FormatProcMem = %q", got)
	}
	ct := time.Date(2026, 7, 25, 13, 45, 12, 0, time.Local)
	if got := FormatCreateTime(ct.UnixMilli()); got != "13:45:12" {
		t.Errorf("FormatCreateTime = %q", got)
	}
}
