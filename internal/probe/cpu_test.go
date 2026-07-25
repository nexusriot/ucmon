package probe

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeZone(t *testing.T, base, name, ztype, temp string) {
	t.Helper()
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if ztype != "" {
		if err := os.WriteFile(filepath.Join(dir, "type"), []byte(ztype+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if temp != "" {
		if err := os.WriteFile(filepath.Join(dir, "temp"), []byte(temp+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadThermalZones(t *testing.T) {
	base := t.TempDir()
	old := thermalBase
	thermalBase = base
	defer func() { thermalBase = old }()

	fakeZone(t, base, "thermal_zone0", "cpu-thermal", "55000") // millidegrees
	fakeZone(t, base, "thermal_zone1", "gpu-thermal", "48")    // already degrees
	fakeZone(t, base, "thermal_zone2", "broken", "junk")       // unparsable — skipped
	fakeZone(t, base, "thermal_zone3", "insane", "999000")     // >150°C — filtered
	fakeZone(t, base, "cooling_device0", "fan", "1")           // not a thermal_zone — ignored

	zones := readThermalZones()
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2: %+v", len(zones), zones)
	}
	byLabel := map[string]float64{}
	for _, z := range zones {
		byLabel[z.Label] = z.Temp
	}
	if byLabel["cpu-thermal"] != 55.0 {
		t.Errorf("cpu-thermal = %v, want 55.0", byLabel["cpu-thermal"])
	}
	if byLabel["gpu-thermal"] != 48.0 {
		t.Errorf("gpu-thermal = %v, want 48.0", byLabel["gpu-thermal"])
	}
}

func TestTempColorThresholds(t *testing.T) {
	defer SetTempThresholds(50, 65, 80) // restore defaults

	SetTempThresholds(40, 60, 70)
	cases := []struct {
		temp float64
		want string
	}{
		{30, "42"},  // green
		{40, "226"}, // yellow at warn
		{60, "214"}, // orange at hot
		{70, "196"}, // red at crit
		{95, "196"},
	}
	for _, c := range cases {
		if got := TempColor(c.temp); got != c.want {
			t.Errorf("TempColor(%v) = %q, want %q", c.temp, got, c.want)
		}
	}

	// Non-positive overrides are ignored.
	SetTempThresholds(0, -1, 0)
	if got := TempColor(40); got != "226" {
		t.Errorf("thresholds changed by zero values: TempColor(40) = %q", got)
	}
}

func TestFormatTemp(t *testing.T) {
	if got := FormatTemp(55.25); got != "55.2°C" {
		t.Errorf("FormatTemp = %q", got)
	}
}

// Smoke test against the real system: SampleCPU must return sane data on
// any Linux host.
func TestSampleCPUSmoke(t *testing.T) {
	snap, err := SampleCPU()
	if err != nil {
		t.Fatalf("SampleCPU: %v", err)
	}
	if len(snap.PerCorePercent) == 0 {
		t.Error("no per-core data")
	}
	if snap.TakenAt.IsZero() {
		t.Error("TakenAt not set")
	}
	if snap.TotalPercent < 0 || snap.TotalPercent > 100 {
		t.Errorf("TotalPercent = %v", snap.TotalPercent)
	}
}
