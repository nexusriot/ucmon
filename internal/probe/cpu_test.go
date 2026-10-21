package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
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

// Smoke test against the real system: a sampler must return sane data on any
// Linux host, and must do it from its own baseline rather than a shared one.
func TestSampleCPUSmoke(t *testing.T) {
	s := NewCPUSampler()
	snap, err := s.Sample()
	if err != nil {
		t.Fatalf("Sample: %v", err)
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
	// The first sample has no baseline, so it reports 0 rather than erroring.
	if snap.TotalPercent != 0 || snap.Breakdown.Valid {
		t.Errorf("first sample claimed an interval: total=%v valid=%v",
			snap.TotalPercent, snap.Breakdown.Valid)
	}

	// /proc/stat counts in 10ms USER_HZ ticks, so two back-to-back reads
	// usually land inside the same tick and diff to nothing. This host hides
	// that because Sample() spends longer than a tick reading thermal zones
	// and cpufreq policies; a CI runner has neither and returns instantly.
	// Burn a window wider than one tick so the interval is always real.
	burnCPU(20 * time.Millisecond)

	snap2, err := s.Sample()
	if err != nil {
		t.Fatalf("second Sample: %v", err)
	}
	if !snap2.Breakdown.Valid {
		t.Error("second sample produced no breakdown")
	}
	if len(snap2.PerCorePercent) != len(snap.PerCorePercent) {
		t.Errorf("core count moved: %d -> %d", len(snap.PerCorePercent), len(snap2.PerCorePercent))
	}
	for i, p := range snap2.PerCorePercent {
		if p < 0 || p > 100 {
			t.Errorf("core %d = %v", i, p)
		}
	}
}

// A second sampler must not disturb the first one's baseline — that is the
// whole point of the type. metrics_listen runs the exporter's sampler on its
// own goroutine while the TUI's keeps ticking.
func TestCPUSamplersAreIndependent(t *testing.T) {
	a, b := NewCPUSampler(), NewCPUSampler()
	if _, err := a.Sample(); err != nil {
		t.Fatalf("a prime: %v", err)
	}
	// The burn goes before b's reads, not after: it has to separate a's two
	// samples without also separating b's last one from a's. Burning
	// afterwards hands a shared baseline the same wide interval, and the test
	// sails through the very regression it names.
	burnCPU(20 * time.Millisecond)
	for i := 0; i < 5; i++ {
		if _, err := b.Sample(); err != nil {
			t.Fatalf("b sample: %v", err)
		}
	}
	snap, err := a.Sample()
	if err != nil {
		t.Fatalf("a sample: %v", err)
	}
	if !snap.Breakdown.Valid {
		t.Fatal("a lost its baseline to b")
	}
}

// Concurrent samplers must be race-free: the exporter and the TUI sample at
// the same time whenever metrics_listen is set.
func TestCPUSamplerConcurrentUse(t *testing.T) {
	s := NewCPUSampler()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if _, err := s.Sample(); err != nil {
					t.Errorf("Sample: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// SetTempThresholds is process-wide and written from the exporter goroutine
// (NewModel applies it per scrape) while the renderer reads it.
func TestTempThresholdsAreRaceFree(t *testing.T) {
	defer SetTempThresholds(50, 65, 80)

	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			SetTempThresholds(40+float64(i%5), 60, 70)
		}
		close(done)
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = TempColor(55)
				_, _, _ = TempThresholds()
			}
		}
	}()
	wg.Wait()
}

func burnCPU(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
	}
}

func TestDiffCPUTimes(t *testing.T) {
	prev := cpu.TimesStat{User: 100, System: 50, Idle: 1000, Iowait: 10, Irq: 1, Softirq: 1, Nice: 0, Steal: 0}
	// Over the interval: 10 user, 5 system, 60 iowait, 25 idle = 100 ticks.
	cur := cpu.TimesStat{User: 110, System: 55, Idle: 1025, Iowait: 70, Irq: 1, Softirq: 1, Nice: 0, Steal: 0}

	b := diffCPUTimes(prev, cur)
	if !b.Valid {
		t.Fatal("two readings produced no breakdown")
	}
	if b.User != 10 || b.System != 5 || b.Iowait != 60 || b.Idle != 25 {
		t.Errorf("split = user %.1f system %.1f iowait %.1f idle %.1f, want 10/5/60/25",
			b.User, b.System, b.Iowait, b.Idle)
	}
	// This is the whole point: a box 75%% "busy" that is really stuck on I/O.
	if b.Busiest != "iowait" {
		t.Errorf("Busiest = %q, want iowait", b.Busiest)
	}
}

// A wholesale counter reset leaves nothing measurable for the interval, so
// the split must report itself invalid rather than invent negative shares.
func TestDiffCPUTimesHandlesCounterReset(t *testing.T) {
	prev := cpu.TimesStat{User: 500, Idle: 500}
	cur := cpu.TimesStat{User: 10, Idle: 20} // counters went backwards

	if b := diffCPUTimes(prev, cur); b.Valid {
		t.Errorf("a full counter reset produced a split: %+v", b)
	}
}

// A core coming online mid-interval moves some counters forward and leaves
// others behind. That is still a usable interval, clamped to 0-100.
func TestDiffCPUTimesHandlesPartialReset(t *testing.T) {
	prev := cpu.TimesStat{User: 100, System: 50, Idle: 1000}
	cur := cpu.TimesStat{User: 110, System: 40, Idle: 1090} // system went backwards

	b := diffCPUTimes(prev, cur)
	if !b.Valid {
		t.Fatal("a partial reset discarded a measurable interval")
	}
	if b.System != 0 {
		t.Errorf("System = %.1f, want 0 for a counter that went backwards", b.System)
	}
	for name, v := range map[string]float64{
		"user": b.User, "system": b.System, "iowait": b.Iowait,
		"nice": b.Nice, "irq": b.IRQ, "steal": b.Steal, "idle": b.Idle,
	} {
		if v < 0 || v > 100 {
			t.Errorf("%s out of range: %.1f", name, v)
		}
	}
}

func TestDiffCPUTimesIdleInterval(t *testing.T) {
	same := cpu.TimesStat{User: 1, Idle: 1}
	if b := diffCPUTimes(same, same); b.Valid {
		t.Error("an interval with no elapsed ticks must not claim a valid split")
	}
}

func TestReadTripPoints(t *testing.T) {
	base := t.TempDir()
	old := thermalBase
	thermalBase = base
	defer func() { thermalBase = old }()

	zone := filepath.Join(base, "thermal_zone0")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"type":              "cpu-thermal\n",
		"temp":              "45000\n",
		"trip_point_0_type": "passive\n",
		"trip_point_0_temp": "80000\n",
		"trip_point_1_type": "critical\n",
		"trip_point_1_temp": "85000\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(zone, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tp, ok := ReadTripPoints()
	if !ok {
		t.Fatal("trip points not found")
	}
	if tp.Passive != 80 || tp.Critical != 85 {
		t.Errorf("passive/critical = %.0f/%.0f, want 80/85", tp.Passive, tp.Critical)
	}
	if tp.Throttle() != 80 {
		t.Errorf("Throttle() = %.0f, want the passive trip at 80", tp.Throttle())
	}
	if tp.Zone != "cpu-thermal" {
		t.Errorf("Zone = %q", tp.Zone)
	}

	warn, hot, crit, ok := TripThresholds(tp)
	if !ok {
		t.Fatal("TripThresholds rejected a zone with both trips")
	}
	if hot != 80 || crit != 85 {
		t.Errorf("hot/crit = %.0f/%.0f, want 80/85", hot, crit)
	}
	if warn >= hot {
		t.Errorf("warn %.0f must sit below hot %.0f", warn, hot)
	}
}

func TestReadTripPointsAbsent(t *testing.T) {
	old := thermalBase
	thermalBase = filepath.Join(t.TempDir(), "nope")
	defer func() { thermalBase = old }()

	if _, ok := ReadTripPoints(); ok {
		t.Error("a missing thermal tree yielded trip points")
	}
}

// A zone that declares only a critical trip still calibrates the colours.
func TestTripThresholdsCriticalOnly(t *testing.T) {
	warn, hot, crit, ok := TripThresholds(TripPoints{Critical: 109})
	if !ok {
		t.Fatal("a critical-only zone was rejected")
	}
	if crit != 109 {
		t.Errorf("crit = %.0f, want 109", crit)
	}
	if !(warn < hot && hot < crit) {
		t.Errorf("thresholds not ordered: warn %.0f hot %.0f crit %.0f", warn, hot, crit)
	}
}

func TestTripThresholdsRejectsEmpty(t *testing.T) {
	if _, _, _, ok := TripThresholds(TripPoints{}); ok {
		t.Error("a zone with no trips produced thresholds")
	}
}

func TestZoneIndexSortsNumerically(t *testing.T) {
	if zoneIndex("thermal_zone10") <= zoneIndex("thermal_zone2") {
		t.Error("zone10 must sort after zone2, not before it lexically")
	}
	if zoneIndex("not_a_zone") == 0 {
		t.Error("an unparseable zone name must sort last, not first")
	}
}

// ReadDir sorts as text, so a host with ten or more thermal zones listed
// zone10 and zone11 between zone1 and zone2 in the fallback sensor path.
func TestReadThermalZonesSortNumerically(t *testing.T) {
	base := t.TempDir()
	old := thermalBase
	thermalBase = base
	defer func() { thermalBase = old }()

	for _, n := range []int{0, 1, 2, 10, 11} {
		fakeZone(t, base, fmt.Sprintf("thermal_zone%d", n),
			fmt.Sprintf("zone%d", n), fmt.Sprintf("%d000", 40+n))
	}

	got := readThermalZones()
	want := []string{"zone0", "zone1", "zone2", "zone10", "zone11"}
	if len(got) != len(want) {
		t.Fatalf("got %d zones, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Label != w {
			labels := make([]string, len(got))
			for j, r := range got {
				labels[j] = r.Label
			}
			t.Fatalf("zone order = %v, want %v", labels, want)
		}
	}
}
