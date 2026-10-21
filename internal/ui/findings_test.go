package ui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// sizedModel returns a model that has seen a window size, so the viewports
// have real dimensions.
func sizedModel(t *testing.T, cfg config.Config, w, h int) Model {
	t.Helper()
	m := NewModel(cfg)
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return next.(Model)
}

// countCmds flattens a (possibly batched) command into its leaf messages.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgs(c)...)
	}
	return out
}

// The socket walk readlinks every /proc/<pid>/fd entry on the host, so it
// needs the same in-flight guard as the process walk it runs beside.
func TestConnectionFetchIsGuarded(t *testing.T) {
	cfg := config.Default()
	m := sizedModel(t, cfg, 100, 30)
	m.procEvery, m.slowEvery = 1, 1000

	// First proc tick dispatches the socket walk and raises its guard.
	next, _ := m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if !m.inFlight[fetchConns] {
		t.Fatal("the connection fetch did not raise its in-flight guard")
	}

	// A second tick while it is still running must skip rather than stack a
	// second walk on top.
	before := m.behindSkips[fetchConns]
	next, _ = m.Update(tickMsg(time.Now()))
	m = next.(Model)
	if m.behindSkips[fetchConns] <= before {
		t.Error("a second tick dispatched a concurrent socket walk")
	}
	if badge := m.behindBadge(); !strings.Contains(badge, "connections") {
		t.Errorf("lateness badge = %q, want it to name the connection walk", badge)
	}

	// The reply lowers the guard again.
	next, _ = m.Update(connMsg(nil))
	if next.(Model).inFlight[fetchConns] {
		t.Error("the connection reply did not lower its guard")
	}
}

// Pause freezes the readings, which is what it is for. Silencing the alert
// engine at the same moment would disarm the monitor exactly when someone
// stopped it to look at something worrying.
func TestPauseFreezesSamplingButNotAlerting(t *testing.T) {
	cfg := config.Default()
	cfg.AlertsEnabled = true
	m := sizedModel(t, cfg, 100, 30)

	// A condition that is unambiguously firing. The threshold has to come
	// from the model's own config: NewModel calibrates it from the board's
	// thermal trip points, so cfg's default 80 is not what alerting compares
	// against on a host that publishes a higher critical trip.
	m.cpuSnap.Temperatures = []probe.TempReading{{Label: "cpu_thermal", Temp: m.cfg.TempCrit + 10}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	if !m.paused {
		t.Fatal("space did not pause")
	}

	before := m.tickCount
	next, cmd := m.Update(tickMsg(time.Now()))
	m = next.(Model)

	if m.tickCount != before {
		t.Error("a paused tick advanced the sample counter")
	}
	msgs := collectMsgs(cmd)
	for _, msg := range msgs {
		switch msg.(type) {
		case tickMsg:
		default:
			t.Errorf("a paused tick dispatched a fetch: %T", msg)
		}
	}

	if _, _, any := m.alerts.summary(); !any {
		t.Error("alerting stopped while paused; the bell and the hook would be silent too")
	}
	if len(m.alerts.log) == 0 {
		t.Error("a critical condition went unlogged while paused")
	}
}

// A keypress colliding with a scheduled round is not sampling lateness, and
// counting it as such lit the header's badge whenever someone held down the
// brightness key.
func TestUserRefreshDoesNotReportLateness(t *testing.T) {
	m := sizedModel(t, config.Default(), 100, 30)
	m.inFlight[fetchSys] = true

	if cmd := m.queueUser(fetchSys, m.fetchSysCmd); cmd != nil {
		t.Error("a user refresh ran a second concurrent round")
	}
	if m.behindSkips[fetchSys] != 0 {
		t.Error("a user refresh was counted as a missed sampling deadline")
	}
	if badge := m.behindBadge(); badge != "" {
		t.Errorf("lateness badge = %q after a user keypress, want none", badge)
	}

	// With nothing in flight it dispatches and takes the guard.
	m.inFlight[fetchSys] = false
	if cmd := m.queueUser(fetchSys, m.fetchSysCmd); cmd == nil {
		t.Error("a user refresh with nothing in flight did not dispatch")
	}
	if !m.inFlight[fetchSys] {
		t.Error("a user refresh did not take the in-flight guard")
	}
}

// The selected interface grows an SSID line once iw data lands, so the
// connection viewport's budget has to move with it or the last row is clipped.
func TestNetViewportRebudgetsWhenIWDataLands(t *testing.T) {
	m := sizedModel(t, config.Default(), 120, 30)
	m.activeTab = tabNet
	m.netSnap = probe.NetSnapshot{TakenAt: time.Now(), Ifaces: []probe.IfaceInfo{
		{Name: "wlan0", IsUp: true, WiFi: probe.WiFiInfo{Present: true, QualityPct: 70, SignalDBm: -50}},
	}}
	m.netIfaceName = "wlan0"
	m.netVP.Height = m.calcNetVPHeight()
	before := m.netVP.Height

	next, _ := m.Update(sysMsg{iwLinks: map[string]probe.IWLink{
		"wlan0": {Present: true, SSID: "Net", Channel: 6},
	}})
	m = next.(Model)

	if m.netVP.Height != before-1 {
		t.Errorf("viewport height %d -> %d; the new SSID line was not budgeted for",
			before, m.netVP.Height)
	}
	assertFitsFrame(t, m, 120, 30)
}

// The modem block is one more thing competing for the Network tab's rows.
func TestNetViewportRebudgetsForModem(t *testing.T) {
	cfg := config.Default()
	cfg.ModemEnabled = true
	m := sizedModel(t, cfg, 120, 30)
	m.activeTab = tabNet
	m.netSnap = probe.NetSnapshot{TakenAt: time.Now(), Ifaces: []probe.IfaceInfo{
		{Name: "wwan0", IsUp: true},
	}}
	before := m.calcNetVPHeight()

	next, _ := m.Update(healthMsg{modem: probe.ModemInfo{
		Present: true, State: "connected", Operator: "Test", SignalPct: 70,
	}})
	m = next.(Model)

	if m.netVP.Height != before-2 {
		t.Errorf("viewport height %d -> %d; the modem block was not budgeted for",
			before, m.netVP.Height)
	}
	if !strings.Contains(m.View(), "Modem") {
		t.Error("the modem block is not rendered")
	}
	assertFitsFrame(t, m, 120, 30)
}

// assertFitsFrame is the invariant every view has to hold: never taller or
// wider than the terminal, or Bubble Tea's alt-screen renderer blanks it.
func assertFitsFrame(t *testing.T, m Model, w, h int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	if len(lines) > h {
		t.Errorf("view is %d lines, terminal is %d", len(lines), h)
	}
	for i, ln := range lines {
		// lipgloss.Width, not len: every line carries ANSI colour codes.
		if got := lipgloss.Width(ln); got > w {
			t.Errorf("line %d is %d columns, terminal is %d: %q", i, got, w, ln)
		}
	}
}

func TestCPUBreakdownNamesIOWait(t *testing.T) {
	// The whole point: "busy" and "blocked on the card" look identical in a
	// single utilisation number.
	line := cpuBreakdownLine(probe.CPUBreakdown{
		Valid: true, User: 5, System: 3, Iowait: 70, Idle: 22, Busiest: "iowait",
	})
	if !strings.Contains(line, "iowait") {
		t.Errorf("breakdown = %q, want an iowait share", line)
	}
	if !strings.Contains(line, "blocked on I/O") {
		t.Errorf("breakdown = %q, want the dominant iowait called out", line)
	}

	busy := cpuBreakdownLine(probe.CPUBreakdown{
		Valid: true, User: 70, System: 8, Iowait: 1, Idle: 21, Busiest: "user",
	})
	if strings.Contains(busy, "blocked on I/O") {
		t.Errorf("a compute-bound split was labelled as I/O-blocked: %q", busy)
	}

	if got := cpuBreakdownLine(probe.CPUBreakdown{Valid: true, Idle: 100}); !strings.Contains(got, "idle") {
		t.Errorf("an idle split = %q", got)
	}
}

func TestStorageHealthLineGradesWear(t *testing.T) {
	worn := storageHealthLine(probe.StorageHealth{
		Device: "mmcblk0", Kind: "eMMC", Name: "BJTD4R", SizeB: 32 << 30, LifeA: 9, PreEOL: 2,
	})
	if !strings.Contains(worn, "90%") {
		t.Errorf("worn card line = %q, want the wear percentage", worn)
	}
	if !strings.Contains(worn, "reserve warning") {
		t.Errorf("worn card line = %q, want the pre-EOL state", worn)
	}

	dead := storageHealthLine(probe.StorageHealth{Device: "mmcblk0", Kind: "SD", ReadOnly: true})
	if !strings.Contains(dead, "READ-ONLY") {
		t.Errorf("retired card line = %q", dead)
	}

	plain := storageHealthLine(probe.StorageHealth{Device: "sda", Kind: "disk", Name: "SSD"})
	if !strings.Contains(plain, "no wear data") {
		t.Errorf("a device with no registers = %q, want it said plainly", plain)
	}
}

func TestIfaceErrTag(t *testing.T) {
	if got := ifaceErrTag(probe.IfaceInfo{Name: "eth0"}); got != "" {
		t.Errorf("a clean interface produced %q", got)
	}

	historic := ifaceErrTag(probe.IfaceInfo{DropIn: 245})
	if !strings.Contains(historic, "245") {
		t.Errorf("historic drops = %q", historic)
	}
	if strings.Contains(historic, "/s") {
		t.Errorf("a link not currently dropping showed a rate: %q", historic)
	}

	active := ifaceErrTag(probe.IfaceInfo{DropIn: 300, DropInPs: 12})
	if !strings.Contains(active, "+12/s") {
		t.Errorf("active drops = %q, want the live rate", active)
	}
}

func TestStorageAlertsFireOnWear(t *testing.T) {
	cfg := config.Default()
	m := NewModel(cfg)
	m.storage = []probe.StorageHealth{
		{Device: "mmcblk0", Kind: "eMMC", LifeA: 9}, // 90%: critical
		{Device: "mmcblk1", Kind: "eMMC", LifeA: 7}, // 70%: warning
		{Device: "nvme0n1", Kind: "NVMe", LifeA: 1}, // fine
	}

	conds := m.currentAlertConds()
	if c, ok := conds["storage:mmcblk0"]; !ok || c.sev != sevCrit {
		t.Errorf("a 90%%-worn card gave %+v (present=%v), want critical", c, ok)
	}
	if c, ok := conds["storage:mmcblk1"]; !ok || c.sev != sevWarn {
		t.Errorf("a 70%%-worn card gave %+v (present=%v), want warning", c, ok)
	}
	if _, ok := conds["storage:nvme0n1"]; ok {
		t.Error("a healthy device raised an alert")
	}
}

func TestModemAlertOnlyWhenEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.ModemEnabled = false
	m := NewModel(cfg)
	m.modem = probe.ModemInfo{Present: true, State: "searching"}
	if _, ok := m.currentAlertConds()["modem"]; ok {
		t.Error("modem alert fired with modem polling disabled")
	}

	cfg.ModemEnabled = true
	m = NewModel(cfg)
	m.modem = probe.ModemInfo{Present: true, State: "searching"}
	if _, ok := m.currentAlertConds()["modem"]; !ok {
		t.Error("an unregistered modem raised no alert")
	}

	m.modem.State = "connected"
	if _, ok := m.currentAlertConds()["modem"]; ok {
		t.Error("a connected modem raised an alert")
	}
}

// Z must zero the peaks and the slopes together: a peak reset that left the
// trend history standing would keep firing predictions from before the reset.
func TestPeaksResetClearsTrends(t *testing.T) {
	m := sizedModel(t, config.Default(), 100, 30)
	m.peaks.cpuPct = 99
	m.peaks.battMin = 12
	now := time.Now()
	m.trends.observe("batt", 90, now.Add(-2*time.Minute))
	m.trends.observe("batt", 50, now)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Z'}})
	m = next.(Model)

	if m.peaks.cpuPct != 0 {
		t.Errorf("cpu peak = %v after reset", m.peaks.cpuPct)
	}
	if m.peaks.battMin != -1 {
		t.Errorf("battery minimum = %d after reset, want the unset sentinel", m.peaks.battMin)
	}
	if _, ok := m.trends.rate("batt"); ok {
		t.Error("the trend history survived a peaks reset")
	}
	if m.notice.get() == "" {
		t.Error("the reset said nothing")
	}
}

func TestProcIOColumn(t *testing.T) {
	m := sizedModel(t, config.Default(), 140, 30)
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{
		{PID: 1, Name: "writer", Status: "S", IOWriteBps: 5 << 20, IOKnown: true},
		{PID: 2, Name: "idle", Status: "S", IOKnown: true},
		{PID: 3, Name: "opaque", Status: "S"},
	}
	m.refreshProcs()

	text := m.renderProcsText()
	if !strings.Contains(text, "MiB/s") {
		t.Errorf("the I/O column is missing a rate:\n%s", text)
	}
	if !strings.Contains(m.buildProcsHeader(), "DISK I/O") {
		t.Error("the header does not name the I/O column")
	}

	// An unreadable counter is a different statement from a confident zero.
	if got := procIOText(probe.ProcInfo{IOKnown: false}); got != "-" {
		t.Errorf("unreadable I/O rendered as %q, want -", got)
	}
	if got := procIOText(probe.ProcInfo{IOKnown: true}); got != "" {
		t.Errorf("a genuinely idle process rendered as %q, want blank", got)
	}
}

func TestProcIOSort(t *testing.T) {
	m := sizedModel(t, config.Default(), 140, 30)
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{
		{PID: 1, Name: "quiet", CPUPct: 90, IOKnown: true},
		{PID: 2, Name: "thrasher", CPUPct: 1, IOWriteBps: 9 << 20, IOKnown: true},
	}

	m.procsSort = sortIO
	m.refreshProcs()
	if m.procsView[0].Name != "thrasher" {
		t.Errorf("I/O sort put %q first", m.procsView[0].Name)
	}

	// The cycle must reach the new mode and come back round.
	seen := map[string]bool{}
	for i := 0; i < int(numProcSorts); i++ {
		seen[m.procsSort.String()] = true
		m.procsSort = (m.procsSort + 1) % numProcSorts
	}
	for _, want := range []string{"CPU", "MEM", "I/O", "PID", "NAME"} {
		if !seen[want] {
			t.Errorf("sort cycle never reached %q", want)
		}
	}
}

// A narrow terminal keeps the process name rather than the I/O column.
func TestProcIOColumnDropsOnNarrowTerminals(t *testing.T) {
	wide := sizedModel(t, config.Default(), 140, 30)
	if _, _, _, _, _, _, colIO, _ := wide.procsCols(); colIO == 0 {
		t.Error("a wide terminal dropped the I/O column")
	}
	narrow := sizedModel(t, config.Default(), 60, 30)
	if _, _, _, _, _, _, colIO, _ := narrow.procsCols(); colIO != 0 {
		t.Error("a 60-column terminal kept the I/O column")
	}
	if !strings.Contains(narrow.buildProcsHeader(), "NAME") {
		t.Error("the narrow layout dropped the name column instead")
	}
}

// The built-in 50/65/80 suits a Raspberry Pi and is wrong by tens of degrees
// elsewhere; the kernel publishes the right numbers next to the sensors.
func TestCalibrateTempsFromTripPoints(t *testing.T) {
	cfg := config.Default()
	cfg.Sanitize()
	before := cfg.TempCrit

	calibrateTemps(&cfg)

	// This host may or may not declare trip points, so the assertion is on
	// the contract rather than on a particular number.
	if tp, ok := probe.ReadTripPoints(); ok {
		warn, hot, crit, _ := probe.TripThresholds(tp)
		if cfg.TempCrit != crit || cfg.TempHot != hot || cfg.TempWarn != warn {
			t.Errorf("thresholds = %v/%v/%v, want the zone's %v/%v/%v",
				cfg.TempWarn, cfg.TempHot, cfg.TempCrit, warn, hot, crit)
		}
	} else if cfg.TempCrit != before {
		t.Errorf("no trip points available, but the threshold moved to %v", cfg.TempCrit)
	}
}

func TestCalibrateTempsRespectsAnExplicitConfig(t *testing.T) {
	cfg := config.Default()
	cfg.TempWarn, cfg.TempHot, cfg.TempCrit = 40, 55, 70
	cfg.TempExplicit = true
	cfg.Sanitize()

	calibrateTemps(&cfg)
	if cfg.TempCrit != 70 || cfg.TempHot != 55 || cfg.TempWarn != 40 {
		t.Errorf("an explicit config was overwritten: %v/%v/%v",
			cfg.TempWarn, cfg.TempHot, cfg.TempCrit)
	}
}

func TestCalibrateTempsCanBeTurnedOff(t *testing.T) {
	cfg := config.Default()
	cfg.TempFromTripPoints = false
	cfg.Sanitize()
	want := cfg.TempCrit

	calibrateTemps(&cfg)
	if cfg.TempCrit != want {
		t.Errorf("temp_from_trip_points = false still calibrated: %v", cfg.TempCrit)
	}
}

func TestWearColorGrading(t *testing.T) {
	// Harsher than filesystem usage: a full filesystem is an inconvenience,
	// a worn-out card is an unbootable device.
	cases := []struct {
		pct  float64
		want string
	}{
		{10, "42"}, {55, "226"}, {75, "214"}, {95, "196"},
	}
	for _, c := range cases {
		if got := wearColor(c.pct); got != c.want {
			t.Errorf("wearColor(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestStorageAlertMessages(t *testing.T) {
	cases := []struct {
		name string
		h    probe.StorageHealth
		want string
	}{
		{"read-only", probe.StorageHealth{Device: "mmcblk0", Kind: "SD", ReadOnly: true}, "read-only"},
		{"urgent", probe.StorageHealth{Device: "mmcblk0", Kind: "eMMC", PreEOL: 3}, "exhausted"},
		{"warning", probe.StorageHealth{Device: "mmcblk0", Kind: "eMMC", PreEOL: 2}, "80%"},
		{"worn", probe.StorageHealth{Device: "mmcblk0", Kind: "eMMC", LifeA: 8}, "80%"},
		{"unknown", probe.StorageHealth{Device: "sda", Kind: "disk"}, "degraded"},
	}
	for _, c := range cases {
		if got := storageAlertMsg(c.h); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not mention %q", c.name, got, c.want)
		}
	}
}

func TestRunOnceJSONProducesAParseableDocument(t *testing.T) {
	cfg := config.Default()
	cfg.PingEnabled = false // keep the test offline and quick
	out, err := RunOnceJSON(cfg)
	if err != nil {
		t.Fatalf("RunOnceJSON: %v", err)
	}
	var snap SnapshotJSON
	if err := jsonUnmarshalString(out, &snap); err != nil {
		t.Fatalf("RunOnceJSON did not produce valid JSON: %v", err)
	}
	if snap.Version != version {
		t.Errorf("version = %q", snap.Version)
	}
	if snap.Timestamp.IsZero() {
		t.Error("snapshot has no timestamp")
	}
	if snap.Memory.TotalB == 0 {
		t.Error("snapshot reports no installed memory")
	}
	if len(snap.Latency) != 0 {
		t.Error("ping_enabled = false still produced latency entries")
	}
}

func TestBoolMetric(t *testing.T) {
	if boolMetric(true) != "1" || boolMetric(false) != "0" {
		t.Error("boolMetric does not emit Prometheus 1/0")
	}
}

// jsonUnmarshalString keeps the JSON import local to this one assertion.
func jsonUnmarshalString(s string, v any) error { return json.Unmarshal([]byte(s), v) }
