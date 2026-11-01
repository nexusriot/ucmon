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

func TestCounterRate(t *testing.T) {
	cases := []struct {
		name      string
		cur, prev uint64
		dt        float64
		want      float64
	}{
		{"normal", 5000, 1000, 2, 2000},
		{"no change", 1000, 1000, 2, 0},
		{"counter reset", 1_000_000, 5_000_000_000, 5, 0},
		{"zero dt", 5000, 1000, 0, 0},
		{"negative dt", 5000, 1000, -1, 0},
	}
	for _, c := range cases {
		if got := CounterRate(c.cur, c.prev, c.dt); got != c.want {
			t.Errorf("%s: CounterRate(%d,%d,%g) = %g, want %g",
				c.name, c.cur, c.prev, c.dt, got, c.want)
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"sshd: vlad@pts/0":            "sshd: vlad@pts/0",
		"\x1b]52;c;aGk=\x07sleep":     "?]52;c;aGk=?sleep",
		"a\tb\nc\x7f\u0085d":          "a?b?c??d",
		"ünïcødé — fine":              "ünïcødé — fine",
		"\x1b[2J\x1b[Hfake root line": "?[2J?[Hfake root line",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}
