package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

func TestTabFromName(t *testing.T) {
	cases := []struct {
		in   string
		want tab
	}{
		{"cpu", tabCPU},
		{"mem", tabMem},
		{"memory", tabMem},
		{"proc", tabProc},
		{"processes", tabProc},
		{"disk", tabDisk},
		{"net", tabNet},
		{"network", tabNet},
		{"power", tabPower},
		{"gpu", tabGPU},
		{"videocore", tabGPU},
		{"alerts", tabAlerts},
		{"bogus", tabCPU}, // default
		{"", tabCPU},
	}
	for _, c := range cases {
		if got := tabFromName(c.in); got != c.want {
			t.Errorf("tabFromName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestAlertsTabKeys(t *testing.T) {
	m := testModel()
	m.activeTab = tabAlerts

	// Put an event in the log first.
	m.alerts.update(condsOf("temp:x"), time.Now())
	if len(m.alerts.log) != 1 {
		t.Fatalf("log = %d", len(m.alerts.log))
	}

	m, _ = press(t, m, key('m'))
	if !m.alerts.muted {
		t.Error("m did not mute")
	}
	if m.alertBanner() != "" {
		t.Error("muted banner should be empty")
	}
	m, _ = press(t, m, key('m'))
	if m.alerts.muted {
		t.Error("m did not unmute")
	}
	if m.alertBanner() == "" {
		t.Error("unmuted banner with an active condition should render")
	}

	m, _ = press(t, m, key('c'))
	if len(m.alerts.log) != 0 {
		t.Error("c did not clear the log")
	}

	if !strings.Contains(m.alertsTabLabel(), "(1)") {
		t.Errorf("tab label missing active count: %q", m.alertsTabLabel())
	}
}

func TestBacklightKeysWithoutDevice(t *testing.T) {
	m := testModel()
	m.activeTab = tabGPU
	m.backlight = probe.BacklightInfo{} // not present

	m, _ = press(t, m, key('+'))
	if m.sysNotice.get() == "" {
		t.Error("+ without a backlight should set a notice")
	}
}

func TestSnapshotKeyWritesReport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := testModel()
	m.memSnap = probe.MemSnapshot{Total: 1 << 30, Used: 1 << 29, UsedPct: 50, TakenAt: time.Now()}

	m, _ = press(t, m, key('w'))
	if m.notice.get() == "" || !strings.Contains(m.notice.get(), "snapshot written") {
		t.Fatalf("notice = %q", m.notice.get())
	}

	matches, err := filepath.Glob(filepath.Join(home, "ucmon-report-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("report files = %v (err %v)", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "Memory") {
		t.Error("report content missing sections")
	}
}

// latencyLines() promises the exact height renderLatency() occupies — the
// viewport math depends on it.
func TestLatencyLinesMatchesRender(t *testing.T) {
	m := testModel() // ping enabled by default

	if got := strings.Count(m.renderLatency(), "\n"); got != m.latencyLines() {
		t.Errorf("empty state: render %d lines, latencyLines %d", got, m.latencyLines())
	}

	m.pings = []probe.PingResult{
		{Target: "gateway", Addr: "192.168.1.1", OK: true, RTT: 3 * time.Millisecond, Method: "icmp"},
		{Target: "1.1.1.1", Addr: "1.1.1.1", OK: false, Method: "tcp"},
	}
	if got := strings.Count(m.renderLatency(), "\n"); got != m.latencyLines() {
		t.Errorf("two targets: render %d lines, latencyLines %d", got, m.latencyLines())
	}

	m.latSampler = nil // disabled
	if m.latencyLines() != 0 {
		t.Errorf("disabled monitor should take 0 lines, got %d", m.latencyLines())
	}
}

func TestPSIColorAndLine(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{{5, "42"}, {15, "226"}, {30, "214"}, {60, "196"}}
	for _, c := range cases {
		if got := psiColor(c.pct); got != c.want {
			t.Errorf("psiColor(%v) = %q, want %q", c.pct, got, c.want)
		}
	}

	m := testModel()
	if m.psiLine(m.psi.CPU, false) != "" {
		t.Error("no PSI data should render nothing")
	}
	m.psi = probe.PSISnapshot{
		Present: true,
		Mem:     probe.PSIRes{HasSome: true, HasFull: true, SomeAvg10: 3.5, FullAvg10: 1.2},
	}
	line := m.psiLine(m.psi.Mem, true)
	if !strings.Contains(line, "3.5%") || !strings.Contains(line, "full") {
		t.Errorf("psiLine = %q", line)
	}
}

func TestWifiHelpers(t *testing.T) {
	strong := probe.WiFiInfo{Present: true, QualityPct: 90, SignalDBm: -40}
	weak := probe.WiFiInfo{Present: true, QualityPct: 10, SignalDBm: -85}
	if wifiColor(strong) != "42" {
		t.Errorf("strong = %q", wifiColor(strong))
	}
	if wifiColor(weak) != "196" {
		t.Errorf("weak = %q", wifiColor(weak))
	}
	if s := wifiSummary(strong); !strings.Contains(s, "90%") || !strings.Contains(s, "-40dBm") {
		t.Errorf("summary = %q", s)
	}
}

func TestStatusStyled(t *testing.T) {
	// Text passes through regardless of the styling profile.
	for _, s := range []string{"Charging", "Discharging", "Full", "Not charging"} {
		if !strings.Contains(statusStyled(s), s) {
			t.Errorf("statusStyled(%q) lost the text", s)
		}
	}
	if !strings.Contains(statusStyled(""), "Unknown") {
		t.Error("empty status should render Unknown")
	}
}
