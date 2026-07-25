package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAfter(t *testing.T) {
	cases := []struct {
		s, prefix, suffix string
		want              float64
	}{
		{"temp=47.2'C", "temp=", "'", 47.2},
		{"volt=0.8625V", "volt=", "V", 0.8625},
		{"no prefix here", "temp=", "'", 0},
		{"temp=junk'C", "temp=", "'", 0},
		{"temp=12.5", "temp=", "'", 12.5}, // missing suffix → rest of string
	}
	for _, c := range cases {
		if got := parseAfter(c.s, c.prefix, c.suffix); got != c.want {
			t.Errorf("parseAfter(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestParseMemMB(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"gpu=76M", 76},
		{"arm=948M", 948},
		{"garbage", 0},
		{"gpu=xM", 0},
	}
	for _, c := range cases {
		if got := parseMemMB(c.s); got != c.want {
			t.Errorf("parseMemMB(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

// fakeVcgencmd puts a stub vcgencmd script on PATH so the full exec+parse
// pipeline runs. Skipped on hosts that carry the real binary (a Pi).
func fakeVcgencmd(t *testing.T) {
	t.Helper()
	for _, p := range []string{"/usr/bin/vcgencmd", "/opt/vc/bin/vcgencmd"} {
		if _, err := os.Stat(p); err == nil {
			t.Skipf("real vcgencmd present at %s", p)
		}
	}
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  measure_temp)  echo "temp=47.2'C" ;;
  measure_volts) echo "volt=0.8625V" ;;
  measure_clock)
    case "$2" in
      arm) echo "frequency(48)=1500000000" ;;
      *)   echo "frequency(1)=0" ;;
    esac ;;
  get_mem)
    case "$2" in
      arm) echo "arm=948M" ;;
      gpu) echo "gpu=76M" ;;
    esac ;;
  get_throttled) echo "throttled=0x50005" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "vcgencmd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSampleGPUWithFakeVcgencmd(t *testing.T) {
	fakeVcgencmd(t)

	g := SampleGPU()
	if !g.Available {
		t.Fatal("Available should be true with vcgencmd on PATH")
	}
	if g.CoreTempC != 47.2 {
		t.Errorf("CoreTempC = %v", g.CoreTempC)
	}
	if g.CoreVolts != 0.8625 {
		t.Errorf("CoreVolts = %v", g.CoreVolts)
	}
	if g.MemARMMB != 948 || g.MemGPUMB != 76 {
		t.Errorf("mem split = %d/%d", g.MemARMMB, g.MemGPUMB)
	}
	// Only the arm domain reports a non-zero clock in the stub.
	if len(g.Clocks) != 1 || g.Clocks[0].Name != "arm" || g.Clocks[0].MHz != 1500 {
		t.Errorf("Clocks = %+v", g.Clocks)
	}
}

func TestReadThrottleWithFakeVcgencmd(t *testing.T) {
	fakeVcgencmd(t)

	th := readThrottle()
	if !th.Available {
		t.Fatal("throttle unavailable")
	}
	// 0x50005: bits 0 (UV now), 2 (throttled now), 16 (UV occurred), 18 (throttled occurred).
	if !th.UnderVoltageNow || !th.ThrottledNow {
		t.Errorf("now flags: %+v", th)
	}
	if th.FreqCappedNow || th.SoftTempLimitNow {
		t.Errorf("unexpected now flags: %+v", th)
	}
	if !th.UnderVoltageOccurred || !th.ThrottledOccurred {
		t.Errorf("occurred flags: %+v", th)
	}
	if th.FreqCappedOccurred || th.SoftTempLimitOccurred {
		t.Errorf("unexpected occurred flags: %+v", th)
	}
}
