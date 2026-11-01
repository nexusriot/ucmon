package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func condsOf(pairs ...string) map[string]alertCond {
	out := map[string]alertCond{}
	for _, k := range pairs {
		out[k] = alertCond{sevWarn, "msg " + k}
	}
	return out
}

func TestAlertEngineRaiseAndDampedClear(t *testing.T) {
	e := newAlertEngine(50, 3)
	now := time.Now()

	events := e.update(condsOf("temp:cpu"), now)
	if len(events) != 1 || events[0].cleared {
		t.Fatalf("raise: got %+v", events)
	}

	// Two absent ticks: still active (damping), no events.
	for i := 0; i < 2; i++ {
		if ev := e.update(condsOf(), now); len(ev) != 0 {
			t.Fatalf("absent tick %d should not emit, got %+v", i+1, ev)
		}
		if _, _, any := e.summary(); !any {
			t.Fatalf("condition cleared after only %d absent ticks", i+1)
		}
	}

	// Third absent tick clears.
	events = e.update(condsOf(), now)
	if len(events) != 1 || !events[0].cleared {
		t.Fatalf("clear: got %+v", events)
	}
	if _, _, any := e.summary(); any {
		t.Error("still active after clear")
	}
}

func TestAlertEngineFlappingStaysActiveSilently(t *testing.T) {
	e := newAlertEngine(50, 3)
	now := time.Now()

	total := 0
	for i := 0; i < 10; i++ { // present, absent, present, absent…
		conds := condsOf()
		if i%2 == 0 {
			conds = condsOf("temp:cpu")
		}
		total += len(e.update(conds, now))
	}
	if total != 1 {
		t.Errorf("flapping condition logged %d events, want just the initial raise", total)
	}
	if _, _, any := e.summary(); !any {
		t.Error("flapping condition should still be active")
	}
}

func TestAlertEngineEscalation(t *testing.T) {
	e := newAlertEngine(50, 1)
	now := time.Now()

	e.update(map[string]alertCond{"batt": {sevWarn, "low"}}, now)
	events := e.update(map[string]alertCond{"batt": {sevCrit, "critical"}}, now)
	if len(events) != 1 || events[0].sev != sevCrit || events[0].cleared {
		t.Fatalf("escalation: got %+v", events)
	}

	// A held de-escalation is announced at the lower severity, so the hook
	// and the log know what the eventual clear refers to.
	if ev := e.update(map[string]alertCond{"batt": {sevWarn, "low"}}, now); len(ev) != 1 || ev[0].sev != sevWarn || ev[0].cleared {
		t.Errorf("de-escalation: got %+v, want one WARN event", ev)
	}
}

func TestAlertEngineClearTicksOne(t *testing.T) {
	e := newAlertEngine(50, 1)
	now := time.Now()
	e.update(condsOf("x"), now)
	events := e.update(condsOf(), now)
	if len(events) != 1 || !events[0].cleared {
		t.Fatalf("clearTicks=1 should clear immediately, got %+v", events)
	}
}

func TestAlertEngineLogCap(t *testing.T) {
	e := newAlertEngine(16, 1)
	now := time.Now()
	for i := 0; i < 30; i++ {
		e.update(condsOf("k"), now) // raise
		e.update(condsOf(), now)    // clear
	}
	if len(e.log) > 16 {
		t.Errorf("log grew to %d, cap is 16", len(e.log))
	}
}

func TestFormatAlertLogLine(t *testing.T) {
	when := time.Date(2026, 7, 25, 12, 30, 0, 0, time.UTC)
	raise := formatAlertLogLine(alertEntry{when: when, sev: sevCrit, msg: "boom"})
	if raise != "2026-07-25 12:30:00 RAISE CRIT boom\n" {
		t.Errorf("raise line = %q", raise)
	}
	clear := formatAlertLogLine(alertEntry{when: when, sev: sevWarn, msg: "ok now", cleared: true})
	if clear != "2026-07-25 12:30:00 CLEAR WARN ok now\n" {
		t.Errorf("clear line = %q", clear)
	}
}

func TestAppendAlertLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.log")
	events := []alertEntry{
		{when: time.Now(), sev: sevWarn, msg: "one"},
		{when: time.Now(), sev: sevCrit, msg: "two"},
	}
	if err := appendAlertLog(path, events); err != nil {
		t.Fatal(err)
	}
	if err := appendAlertLog(path, events[:1]); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Errorf("got %d lines, want 3 (append mode)", len(lines))
	}
}

func TestAlertCmdEnv(t *testing.T) {
	env := alertCmdEnv(alertEntry{sev: sevCrit, msg: "hot"})
	want := []string{"UCMON_ALERT_STATE=raise", "UCMON_ALERT_SEV=CRIT", "UCMON_ALERT_MSG=hot"}
	for i, w := range want {
		if env[i] != w {
			t.Errorf("env[%d] = %q, want %q", i, env[i], w)
		}
	}
	env = alertCmdEnv(alertEntry{sev: sevWarn, msg: "x", cleared: true})
	if env[0] != "UCMON_ALERT_STATE=clear" {
		t.Errorf("cleared state env = %q", env[0])
	}
}

func TestLossPct(t *testing.T) {
	if got := lossPct(nil); got != 0 {
		t.Errorf("empty window = %v", got)
	}
	if got := lossPct([]bool{true, true, false, false}); got != 50 {
		t.Errorf("2/4 lost = %v, want 50", got)
	}
}

func TestCurrentAlertCondsClassicConditions(t *testing.T) {
	cfg := config.Default()
	// Pin the thresholds: without this the model would calibrate them from
	// whatever thermal trip points the host running the tests declares.
	cfg.TempExplicit = true
	m := NewModel(cfg)

	m.cpuSnap = probe.CPUSnapshot{
		Temperatures: []probe.TempReading{
			{Label: "cpu_thermal", Temp: 85}, // ≥ crit 80
			{Label: "gpu_thermal", Temp: 70}, // ≥ hot 65 → warn
			{Label: "nvme", Temp: 40},        // fine
		},
	}
	m.memSnap = probe.MemSnapshot{Total: 1 << 30, UsedPct: 95, SwapTotal: 1 << 30, SwapUsedPct: 60}
	m.disks = []probe.DiskInfo{
		{MountPoint: "/", FSType: "ext4", UsedPct: 95},
		{MountPoint: "/snap", FSType: "squashfs", UsedPct: 100}, // skipped by design
		{MountPoint: "/media/cd", FSType: "iso9660", UsedPct: 100},
	}
	m.powerSnap = probe.PowerSnapshot{
		Load:    probe.LoadInfo{Load1: 9.0, CPUCount: 4}, // ≥ 2.0×4
		Battery: probe.BatteryInfo{Present: true, Capacity: 4, Status: "Discharging"},
		Throttle: probe.ThrottleInfo{
			Available: true, UnderVoltageNow: true, FreqCappedNow: true,
		},
	}

	conds := m.currentAlertConds()

	if c, ok := conds["temp:cpu_thermal"]; !ok || c.sev != sevCrit {
		t.Errorf("crit temp: %+v ok=%v", c, ok)
	}
	if c, ok := conds["temp:gpu_thermal"]; !ok || c.sev != sevWarn {
		t.Errorf("warn temp: %+v ok=%v", c, ok)
	}
	if _, ok := conds["temp:nvme"]; ok {
		t.Error("cool sensor flagged")
	}
	if _, ok := conds["mem"]; !ok {
		t.Error("mem not flagged")
	}
	if _, ok := conds["swap"]; !ok {
		t.Error("swap not flagged")
	}
	if _, ok := conds["disk:/"]; !ok {
		t.Error("full disk not flagged")
	}
	if _, ok := conds["disk:/snap"]; ok {
		t.Error("squashfs must be skipped")
	}
	if _, ok := conds["disk:/media/cd"]; ok {
		t.Error("a mounted ISO is full by design and must be skipped")
	}
	if c, ok := conds["batt"]; !ok || c.sev != sevCrit {
		t.Errorf("battery: %+v ok=%v", c, ok)
	}
	if c, ok := conds["throttle:uv"]; !ok || c.sev != sevCrit {
		t.Errorf("under-voltage: %+v ok=%v", c, ok)
	}
	if c, ok := conds["throttle:cap"]; !ok || c.sev != sevWarn {
		t.Errorf("freq cap: %+v ok=%v", c, ok)
	}
	if _, ok := conds["load"]; !ok {
		t.Error("load not flagged")
	}

	// A charging battery at the same level must not alert.
	m.powerSnap.Battery.Status = "Charging"
	if _, ok := m.currentAlertConds()["batt"]; ok {
		t.Error("charging battery flagged")
	}
}

func TestCurrentAlertCondsNewConditions(t *testing.T) {
	cfg := config.Default()
	cfg.WifiLowDBm = -70
	cfg.PingLossWarnPct = 20
	cfg.PSIWarnPct = 30
	m := NewModel(cfg)

	m.netSnap = probe.NetSnapshot{
		TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{{
			Name: "wlan0", IsUp: true,
			WiFi: probe.WiFiInfo{Present: true, SignalDBm: -80},
		}},
	}
	win := make([]bool, 10)
	for i := range win {
		win[i] = i%2 == 0 // 50% loss
	}
	m.lossWin["gateway"] = win
	m.psi = probe.PSISnapshot{
		Present: true,
		CPU:     probe.PSIRes{HasSome: true, SomeAvg10: 45},
	}

	conds := m.currentAlertConds()
	if _, ok := conds["wifi:wlan0"]; !ok {
		t.Errorf("weak wifi signal not flagged: %v", conds)
	}
	if _, ok := conds["ping:gateway"]; !ok {
		t.Errorf("packet loss not flagged: %v", conds)
	}
	if _, ok := conds["psi:cpu"]; !ok {
		t.Errorf("cpu pressure not flagged: %v", conds)
	}

	// Short loss window must not alert (needs ≥ 10 samples).
	m.lossWin["gateway"] = []bool{false, false}
	if _, ok := m.currentAlertConds()["ping:gateway"]; ok {
		t.Error("loss alert fired on an unsettled window")
	}
}

// A reading wobbling across the crit line must not re-raise CRIT (bell,
// alert_cmd, log) on every crossing: de-escalation is damped like a clear.
func TestAlertDeescalationDamped(t *testing.T) {
	e := newAlertEngine(100, 3)
	now := time.Now()
	crit := map[string]alertCond{"temp:x": {sevCrit, "hot"}}
	warn := map[string]alertCond{"temp:x": {sevWarn, "warm"}}

	if ev := e.update(crit, now); len(ev) != 1 {
		t.Fatalf("first crit: %d events, want 1", len(ev))
	}
	for i := 0; i < 10; i++ {
		if ev := e.update(warn, now); len(ev) != 0 {
			t.Fatalf("wobble %d down: %v", i, ev)
		}
		if c := e.active["temp:x"].cond; c.sev != sevCrit || c.msg != "hot" {
			t.Fatalf("while damped the alert reads %v %q, want what was raised", c.sev, c.msg)
		}
		if ev := e.update(crit, now); len(ev) != 0 {
			t.Fatalf("wobble %d up re-raised: %v", i, ev)
		}
	}

	// A sustained drop does de-escalate — announced, so the hook and the
	// log know the state the eventual clear refers to — and a later climb
	// raises again.
	var down []alertEntry
	for i := 0; i < 3; i++ {
		down = append(down, e.update(warn, now)...)
	}
	if len(down) != 1 || down[0].sev != sevWarn || down[0].msg != "warm" || down[0].cleared {
		t.Fatalf("sustained drop events = %+v, want one WARN \"warm\"", down)
	}
	if got := e.active["temp:x"].cond.sev; got != sevWarn {
		t.Fatalf("sev after sustained warn = %v, want warn", got)
	}
	if ev := e.update(crit, now); len(ev) != 1 {
		t.Fatalf("re-escalation: %d events, want 1", len(ev))
	}
}

// errors=remount-ro is the kernel locking a filesystem after I/O errors —
// on an SD-booted board, usually the card dying. It must raise a critical
// and show on every surface: the Disk tab, the report, JSON and /metrics.
func TestRemountedReadOnlyIsCritical(t *testing.T) {
	m := testModel()
	m.disks = []probe.DiskInfo{
		{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4", Total: 100, Used: 50, UsedPct: 50, ReadOnly: true, RemountedRO: true},
		{Device: "/dev/mmcblk0p1", MountPoint: "/boot/firmware", FSType: "vfat", Total: 100, Used: 10, UsedPct: 10, ReadOnly: true},
	}

	conds := m.currentAlertConds()
	c, ok := conds["diskro:/"]
	if !ok || c.sev != sevCrit || !strings.Contains(c.msg, "read-only") {
		t.Fatalf("diskro:/ = %+v, %v; want a critical", c, ok)
	}
	if _, ok := conds["diskro:/boot/firmware"]; ok {
		t.Error("a mount that is read-only by design raised the remount alert")
	}

	m.activeTab = tabDisk
	if v := m.viewDisk(); !strings.Contains(v, "REMOUNTED READ-ONLY") || !strings.Contains(v, "vfat ro") {
		t.Error("Disk tab does not mark the remounted filesystem")
	}
	if r := m.buildReport(time.Now()); !strings.Contains(r, "[REMOUNTED READ-ONLY]") || !strings.Contains(r, "[ro]") {
		t.Error("report does not mark read-only mounts")
	}
	js := m.snapshotJSON(time.Now())
	if len(js.Disks) != 2 || !js.Disks[0].RemountedRO || js.Disks[1].RemountedRO || !js.Disks[1].ReadOnly {
		t.Errorf("JSON disks = %+v", js.Disks)
	}
	p := m.buildPrometheus()
	for _, want := range []string{
		`ucmon_filesystem_remounted_readonly{mount="/",fstype="ext4"} 1`,
		`ucmon_filesystem_remounted_readonly{mount="/boot/firmware",fstype="vfat"} 0`,
		`ucmon_filesystem_readonly{mount="/boot/firmware",fstype="vfat"} 1`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("/metrics lacks %q", want)
		}
	}
}
