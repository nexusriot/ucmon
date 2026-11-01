package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func TestTrendRateNeedsASettledBaseline(t *testing.T) {
	tr := newTrendTracker()
	now := time.Now()

	tr.observe("x", 100, now)
	if _, ok := tr.rate("x"); ok {
		t.Error("a single sample produced a rate")
	}

	// Two samples a few seconds apart: a real change divided by a tiny
	// interval would read as a dramatic slope built out of noise.
	tr.observe("x", 90, now.Add(5*time.Second))
	if _, ok := tr.rate("x"); ok {
		t.Error("a 5s baseline produced a rate")
	}

	tr.observe("x", 50, now.Add(60*time.Second))
	perHour, ok := tr.rate("x")
	if !ok {
		t.Fatal("a 60s baseline produced no rate")
	}
	// 100 -> 50 over a minute is -3000/hour.
	if perHour > -2900 || perHour < -3100 {
		t.Errorf("rate = %.0f/h, want about -3000/h", perHour)
	}
}

func TestTrendETA(t *testing.T) {
	tr := newTrendTracker()
	now := time.Now()
	// 80% to 30% over five minutes is 10%/min, so 30% of charge lasts 3 minutes.
	tr.observe("batt", 80, now)
	tr.observe("batt", 30, now.Add(5*time.Minute))

	eta, ok := tr.etaTo("batt", 30, 0)
	if !ok {
		t.Fatal("no ETA for a steadily draining battery")
	}
	if eta < 2*time.Minute+30*time.Second || eta > 3*time.Minute+30*time.Second {
		t.Errorf("ETA = %v, want about 3 minutes", eta)
	}

	// A reading moving away from the target never arrives.
	if _, ok := tr.etaTo("batt", 70, 100); ok {
		t.Error("a draining battery produced an ETA to full")
	}
}

func TestTrendDropsSamplesOutsideTheWindow(t *testing.T) {
	tr := newTrendTracker()
	now := time.Now()
	for i := 0; i < 40; i++ {
		tr.observe("x", float64(i), now.Add(-time.Duration(40-i)*10*time.Second))
	}
	// 40 samples ten seconds apart span nearly seven minutes; only the last
	// two minutes may be retained.
	pts := tr.series["x"]
	span := pts[len(pts)-1].at.Sub(pts[0].at)
	if span > trendWindow+15*time.Second {
		t.Errorf("retained %v of history, want about %v", span, trendWindow)
	}
	if len(pts) < 2 {
		t.Error("eviction emptied the series")
	}
}

func TestTrendReset(t *testing.T) {
	tr := newTrendTracker()
	now := time.Now()
	tr.observe("x", 1, now)
	tr.observe("x", 2, now.Add(time.Minute))
	tr.reset()
	if _, ok := tr.rate("x"); ok {
		t.Error("reset left a usable rate behind")
	}
}

func TestHumanETA(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1 min"},
		{20 * time.Minute, "20 min"},
		{3 * time.Hour, "3.0 h"},
		{72 * time.Hour, "3 days"},
	}
	for _, c := range cases {
		if got := humanETA(c.d); got != c.want {
			t.Errorf("humanETA(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// A battery draining fast enough to matter must warn while there is still
// charge to act on — the flat batt_low_pct alert only speaks at 15%.
func TestBatteryDrainRateAlert(t *testing.T) {
	cfg := config.Default()
	cfg.BattDrainWarnPctH = 25
	m := NewModel(cfg)

	now := time.Now()
	m.powerSnap.Battery = probe.BatteryInfo{Present: true, Capacity: 60, Status: "Discharging"}
	m.trends.observe("batt", 80, now.Add(-6*time.Minute))
	m.trends.observe("batt", 60, now)

	conds := m.currentAlertConds()
	c, ok := conds["batt:rate"]
	if !ok {
		t.Fatalf("no drain alert for 200%%/h; got %v", keysOf(conds))
	}
	if !strings.Contains(c.msg, "empty in") {
		t.Errorf("drain message gives no ETA: %q", c.msg)
	}

	// A pack on the charger is not draining, whatever the history says.
	m.powerSnap.Battery.Status = "Charging"
	if _, ok := m.currentAlertConds()["batt:rate"]; ok {
		t.Error("a charging battery raised a drain alert")
	}
}

func TestBatteryDrainRateAlertDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.BattDrainWarnPctH = 0
	m := NewModel(cfg)

	now := time.Now()
	m.powerSnap.Battery = probe.BatteryInfo{Present: true, Capacity: 60, Status: "Discharging"}
	m.trends.observe("batt", 95, now.Add(-2*time.Minute))
	m.trends.observe("batt", 60, now)

	if _, ok := m.currentAlertConds()["batt:rate"]; ok {
		t.Error("batt_drain_warn_pct_h = 0 still raised a drain alert")
	}
}

// On a small card the gap between "90% full" and "full" can be minutes, so
// the projection has to fire on the slope, not the level.
func TestDiskFillProjectionAlert(t *testing.T) {
	cfg := config.Default()
	cfg.DiskFullWarnHours = 6
	m := NewModel(cfg)

	now := time.Now()
	m.disks = []probe.DiskInfo{{MountPoint: "/", FSType: "ext4", UsedPct: 70}}
	m.trends.observe("disk:/", 60, now.Add(-2*time.Minute))
	m.trends.observe("disk:/", 70, now) // 5%/min -> full in ~6 minutes

	c, ok := m.currentAlertConds()["diskfill:/"]
	if !ok {
		t.Fatal("no fill projection for a filesystem gaining 5%/min")
	}
	if c.sev != sevCrit {
		t.Errorf("severity = %v, want critical for under an hour", c.sev)
	}
	if !strings.Contains(c.msg, "full in") {
		t.Errorf("message = %q, want an ETA", c.msg)
	}

	// A filesystem that is not growing must stay quiet even when it is full
	// enough for the level alert to speak.
	m2 := NewModel(cfg)
	m2.disks = []probe.DiskInfo{{MountPoint: "/", FSType: "ext4", UsedPct: 95}}
	m2.trends.observe("disk:/", 95, now.Add(-2*time.Minute))
	m2.trends.observe("disk:/", 95, now)
	if _, ok := m2.currentAlertConds()["diskfill:/"]; ok {
		t.Error("a static filesystem raised a fill projection")
	}
}

func TestTempRiseAlert(t *testing.T) {
	cfg := config.Default()
	cfg.TempRiseWarnCPerMin = 3
	cfg.TempExplicit = true // do not calibrate from the test host's trip points
	m := NewModel(cfg)

	now := time.Now()
	m.cpuSnap.Temperatures = []probe.TempReading{{Label: "cpu_thermal", Temp: 60}}
	m.trends.observe("temp:cpu_thermal", 45, now.Add(-2*time.Minute))
	m.trends.observe("temp:cpu_thermal", 60, now) // 7.5 °C/min

	c, ok := m.currentAlertConds()["temprise:cpu"]
	if !ok {
		t.Fatal("no rise alert for a sensor climbing 7.5 C/min")
	}
	if !strings.Contains(c.msg, "rising") {
		t.Errorf("message = %q", c.msg)
	}

	// A sensor already over the critical threshold is the level alert's job.
	m.cpuSnap.Temperatures = []probe.TempReading{{Label: "cpu_thermal", Temp: m.cfg.TempCrit + 5}}
	if _, ok := m.currentAlertConds()["temprise:cpu"]; ok {
		t.Error("an already-critical sensor also raised a rise alert")
	}
}

// What a 16-core desktop does under ordinary load, measured: the cores jump
// ten degrees in half a minute and level off. That is not a failed fan, and
// eight cores doing it together must not raise eight alerts.
func TestTempRiseIgnoresWarmUpAndGroupsByChip(t *testing.T) {
	cfg := config.Default()
	cfg.TempRiseWarnCPerMin = 3
	cfg.TempExplicit = true
	cfg.TempCrit = 100
	m := NewModel(cfg)
	now := time.Now()

	var temps []probe.TempReading
	for i := range 8 {
		label := fmt.Sprintf("coretemp_core_%d", i)
		temps = append(temps, probe.TempReading{Label: label, Temp: 62})
		// 52 °C, a burst to 62 within 30 s, then flat for a minute.
		m.trends.observe("temp:"+label, 52, now.Add(-90*time.Second))
		m.trends.observe("temp:"+label, 62, now.Add(-60*time.Second))
		m.trends.observe("temp:"+label, 62, now)
	}
	m.cpuSnap.Temperatures = temps
	for k := range m.currentAlertConds() {
		if strings.HasPrefix(k, "temprise:") {
			t.Errorf("a warm-up burst raised %s", k)
		}
	}

	// A real climb on every core is one alert, naming the fastest sensor.
	for i := range 8 {
		label := fmt.Sprintf("coretemp_core_%d", i)
		m.trends.series["temp:"+label] = nil
		m.trends.observe("temp:"+label, 60, now.Add(-2*time.Minute))
		m.trends.observe("temp:"+label, 80+float64(i), now)
		temps[i].Temp = 80 + float64(i)
	}
	var rises []string
	for k, c := range m.currentAlertConds() {
		if strings.HasPrefix(k, "temprise:") {
			rises = append(rises, k+" "+c.msg)
		}
	}
	if len(rises) != 1 || !strings.Contains(rises[0], "temprise:coretemp coretemp_core_7") || !strings.Contains(rises[0], "+7 more coretemp sensors") {
		t.Errorf("rise alerts = %q, want one for the chip, naming its fastest core", rises)
	}
}

// A climb whose critical is far off is not warned about: past the horizon,
// a straight line through a few minutes of readings is a guess.
func TestTempRiseNeedsCriticalWithinTheHorizon(t *testing.T) {
	cfg := config.Default()
	cfg.TempRiseWarnCPerMin = 3
	cfg.TempExplicit = true
	cfg.TempCrit = 120
	m := NewModel(cfg)
	now := time.Now()
	m.cpuSnap.Temperatures = []probe.TempReading{{Label: "pkg", Temp: 60}}
	m.trends.observe("temp:pkg", 52, now.Add(-2*time.Minute))
	m.trends.observe("temp:pkg", 60, now) // 4 °C/min: 120 °C in 15 min
	if c, ok := m.currentAlertConds()["temprise:pkg"]; ok {
		t.Errorf("a climb 15 minutes from critical warned: %q", c.msg)
	}
}

// recordTrend must keep feeding the tracker while paused, or a frozen
// reading would leave a stale slope standing.
func TestRecordTrendRunsWhilePaused(t *testing.T) {
	m := NewModel(config.Default())
	m.paused = true
	m.memSnap = probe.MemSnapshot{Total: 1000, UsedPct: 42}

	now := time.Now()
	m.recordTrend(now)
	m.recordTrend(now.Add(time.Minute))

	if len(m.trends.series["mem"]) != 2 {
		t.Errorf("recordTrend stored %d samples while paused, want 2", len(m.trends.series["mem"]))
	}
	if r, ok := m.trends.rate("mem"); !ok || r != 0 {
		t.Errorf("a frozen reading gave rate %v (ok=%v), want a flat 0", r, ok)
	}
}

func keysOf(m map[string]alertCond) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Capacity is an integer: one 1% step in a short window used to read as
// 30%/h and warn on every step of a healthy 10%/h discharge.
func TestBatteryDrainIgnoresSingleStep(t *testing.T) {
	cfg := config.Default()
	cfg.BattDrainWarnPctH = 25
	m := NewModel(cfg)
	m.powerSnap.Battery = probe.BatteryInfo{Present: true, Capacity: 59, Status: "Discharging"}

	now := time.Now()
	for i := 0; i <= 10; i++ { // 60% for ten minutes, then one step down
		v := 60.0
		if i == 10 {
			v = 59
		}
		m.trends.observe("batt", v, now.Add(time.Duration(i-10)*time.Minute))
	}
	if c, ok := m.currentAlertConds()["batt:rate"]; ok {
		t.Errorf("a single 1%% step raised %q", c.msg)
	}
}

// A barely-moving disk gives an ETA of millions of hours; converting that to
// a Duration overflowed negative on amd64 and read as "full in <1 min" CRIT.
func TestDiskFillETAHugeIsNoAlert(t *testing.T) {
	tr := newTrendTracker()
	now := time.Now()
	tr.observe("disk:/", 50, now.Add(-2*time.Minute))
	tr.observe("disk:/", 50.0000001, now)
	if eta, ok := tr.etaTo("disk:/", 50.0000001, 100); ok {
		t.Errorf("ETA %v for an effectively flat disk", eta)
	}
}

// After a suspend the monotonic clock has not moved, but the wall clock and
// the battery have. The gap must start the series over, not read as a
// fifteen-percent drop in a few minutes.
func TestTrendRestartsAfterAGap(t *testing.T) {
	tr := newTrendTracker()
	t0 := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	for i := range 11 {
		tr.observe("batt", 80, t0.Add(time.Duration(i)*time.Minute))
	}
	// Eight hours of sleep, 15% gone.
	wake := t0.Add(8 * time.Hour)
	tr.observe("batt", 65, wake)
	if _, ok := tr.rate("batt"); ok {
		t.Error("a rate was computed across the suspend gap")
	}
	if len(tr.series["batt"]) != 1 {
		t.Errorf("series kept %d points across the gap", len(tr.series["batt"]))
	}

	// A filesystem gone for longer than its window is forgotten.
	tr.observe("disk:/media/usb", 10, t0)
	tr.evict(wake)
	if _, ok := tr.series["disk:/media/usb"]; ok {
		t.Error("a vanished mount's series was kept")
	}
	if _, ok := tr.series["batt"]; !ok {
		t.Error("a live series was evicted")
	}
}
