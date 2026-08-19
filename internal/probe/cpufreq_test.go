package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakePolicy(t *testing.T, base, name string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for f, content := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(content+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadCPUFreq(t *testing.T) {
	base := t.TempDir()
	old := cpufreqBase
	cpufreqBase = base
	defer func() { cpufreqBase = old }()

	fakePolicy(t, base, "policy0", map[string]string{
		"scaling_cur_freq":            "1500000",
		"cpuinfo_min_freq":            "600000",
		"cpuinfo_max_freq":            "1800000",
		"affected_cpus":               "0 1 2 3",
		"scaling_governor":            "ondemand",
		"scaling_available_governors": "conservative ondemand performance",
	})
	fakePolicy(t, base, "not-a-policy", map[string]string{"scaling_cur_freq": "1"})

	freqs := ReadCPUFreq()
	if len(freqs) != 1 {
		t.Fatalf("got %d policies, want 1", len(freqs))
	}
	f := freqs[0]
	if f.Policy != "policy0" {
		t.Errorf("Policy = %q", f.Policy)
	}
	if f.CurMHz != 1500 || f.MinMHz != 600 || f.MaxMHz != 1800 {
		t.Errorf("MHz = %v/%v/%v, want 1500/600/1800", f.CurMHz, f.MinMHz, f.MaxMHz)
	}
	if f.Governor != "ondemand" {
		t.Errorf("Governor = %q", f.Governor)
	}
	if len(f.Available) != 3 || f.Available[2] != "performance" {
		t.Errorf("Available = %v", f.Available)
	}
	if f.CPUs != "0 1 2 3" {
		t.Errorf("CPUs = %q", f.CPUs)
	}
	if !f.Writable {
		t.Error("temp-dir governor file should be writable")
	}
}

func TestReadCPUFreqAbsent(t *testing.T) {
	old := cpufreqBase
	cpufreqBase = filepath.Join(t.TempDir(), "nope")
	defer func() { cpufreqBase = old }()

	if freqs := ReadCPUFreq(); freqs != nil {
		t.Errorf("expected nil on hosts without cpufreq, got %v", freqs)
	}
}

func TestNextGovernor(t *testing.T) {
	avail := []string{"conservative", "ondemand", "performance"}
	cases := []struct{ cur, want string }{
		{"conservative", "ondemand"},
		{"ondemand", "performance"},
		{"performance", "conservative"}, // wraps
		{"schedutil", "conservative"},   // unknown falls back to first
	}
	for _, c := range cases {
		if got := NextGovernor(c.cur, avail); got != c.want {
			t.Errorf("NextGovernor(%q) = %q, want %q", c.cur, got, c.want)
		}
	}
	if got := NextGovernor("ondemand", nil); got != "ondemand" {
		t.Errorf("empty avail should return cur, got %q", got)
	}
}

func TestSetGovernor(t *testing.T) {
	base := t.TempDir()
	old := cpufreqBase
	cpufreqBase = base
	defer func() { cpufreqBase = old }()

	fakePolicy(t, base, "policy0", map[string]string{"scaling_governor": "ondemand"})

	if err := SetGovernor("policy0", "performance"); err != nil {
		t.Fatalf("SetGovernor: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(base, "policy0", "scaling_governor"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "performance" {
		t.Errorf("governor file = %q", string(b))
	}

	if err := SetGovernor("", "x"); err == nil {
		t.Error("empty policy should error")
	}
}

// A read-only scaling_governor is what a non-root user meets on a real board.
// The error must name the path so the UI hint can point at the udev fix.
func TestSetGovernorPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the write would succeed")
	}
	base := t.TempDir()
	old := cpufreqBase
	cpufreqBase = base
	defer func() { cpufreqBase = old }()

	dir := filepath.Join(base, "policy0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "scaling_governor")
	if err := os.WriteFile(path, []byte("ondemand\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	err := SetGovernor("policy0", "performance")
	if err == nil {
		t.Fatal("writing a read-only governor file succeeded")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error = %v, want a permission-denied message", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %v, want it to name %s", err, path)
	}
}

// A missing policy is a plain error, and empty arguments are rejected before
// any filesystem access.
func TestSetGovernorRejectsBadInput(t *testing.T) {
	base := t.TempDir()
	old := cpufreqBase
	cpufreqBase = base
	defer func() { cpufreqBase = old }()

	for _, c := range []struct{ policy, gov string }{
		{"", "performance"},
		{"policy0", ""},
		{"policy-missing", "performance"},
	} {
		if err := SetGovernor(c.policy, c.gov); err == nil {
			t.Errorf("SetGovernor(%q, %q) succeeded", c.policy, c.gov)
		}
	}
}
