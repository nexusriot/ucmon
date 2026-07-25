package probe

import (
	"testing"
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
