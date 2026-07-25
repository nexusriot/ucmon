package probe

import (
	"os"
	"testing"
)

// The sampler must reuse Process handles between refreshes — that is what
// makes gopsutil report interval CPU% instead of a lifetime average.
func TestProcSamplerReusesHandles(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("first List: %v", err)
	}

	self := int32(os.Getpid())
	first, ok := s.procs[self]
	if !ok {
		t.Fatalf("own pid %d not tracked after List", self)
	}

	if _, err := s.List(); err != nil {
		t.Fatalf("second List: %v", err)
	}
	second, ok := s.procs[self]
	if !ok {
		t.Fatalf("own pid %d dropped on second List", self)
	}
	if first != second {
		t.Error("Process handle was recreated between samples; CPU%% would fall back to lifetime average")
	}
}

func TestProcSamplerEvictsExitedPIDs(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("List: %v", err)
	}

	// Inject an impossible PID and make sure the next refresh drops it.
	s.procs[-4242] = nil
	if _, err := s.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := s.procs[-4242]; ok {
		t.Error("stale pid survived eviction")
	}
}

func TestRuneTrunc(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"héllo wörld", 4, "héll"}, // must not split multi-byte runes
		{"abc", 0, ""},
	}
	for _, c := range cases {
		if got := runeTrunc(c.in, c.n); got != c.want {
			t.Errorf("runeTrunc(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestClampNice(t *testing.T) {
	cases := [][2]int{{-30, -20}, {-20, -20}, {0, 0}, {19, 19}, {25, 19}}
	for _, c := range cases {
		if got := ClampNice(c[0]); got != c[1] {
			t.Errorf("ClampNice(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}
