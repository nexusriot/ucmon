package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// geometryFixture is a model with every tab holding renderable data, sized to
// w×h. Populating all of them matters: the tab views are otherwise never
// exercised, which is how the Disk-tab separator panic went unnoticed.
func geometryFixture(t *testing.T, w, h int) Model {
	t.Helper()
	m := NewModel(config.Default())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m = mm.(Model)

	m.cpuSnap = probe.CPUSnapshot{
		TakenAt:        nonZeroTime(),
		TotalPercent:   42,
		PerCorePercent: []float64{10, 20, 30, 40},
		Temperatures:   []probe.TempReading{{Label: "cpu_thermal", Temp: 61.5}},
		Freqs:          []probe.PolicyFreq{{Policy: "policy0", CurMHz: 1500, MinMHz: 600, MaxMHz: 1800, Governor: "ondemand", CPUs: "0 1 2 3"}},
	}
	m.memSnap = probe.MemSnapshot{TakenAt: nonZeroTime(), Total: 4 << 30, Used: 2 << 30, UsedPct: 50, SwapTotal: 1 << 30, SwapUsed: 1 << 28, SwapUsedPct: 25}
	m.powerSnap = probe.PowerSnapshot{
		TakenAt:  nonZeroTime(),
		Load:     probe.LoadInfo{Load1: 1.2, Load5: 0.9, Load15: 0.7, CPUCount: 4},
		Battery:  probe.BatteryInfo{Present: true, Name: "axp20x-battery", Capacity: 63, Status: "Discharging", VoltageV: 3.9, PowerW: 4.2, HealthPct: 91},
		Throttle: probe.ThrottleInfo{Available: true, Raw: 0x50005, UnderVoltageNow: true},
		ACKnown:  true,
	}
	m.psi = probe.PSISnapshot{Present: true, CPU: probe.PSIRes{HasSome: true, SomeAvg10: 3.2}}
	m.disks = []probe.DiskInfo{
		{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4", Total: 1 << 34, Used: 1 << 33, Free: 1 << 33, UsedPct: 50},
		{Device: "/dev/mmcblk0p1", MountPoint: "/boot/firmware", FSType: "vfat", Total: 1 << 29, Used: 1 << 26, Free: 1 << 28, UsedPct: 12},
	}
	m.diskIOs = []probe.DiskIOInfo{{Device: "mmcblk0", ReadBps: 12345, WriteBps: 6789}}
	m.netSnap = probe.NetSnapshot{TakenAt: nonZeroTime(), Hostname: "uconsole", Ifaces: []probe.IfaceInfo{
		{Name: "wlan0", IsUp: true, Addrs: []string{"192.168.88.42/24"}, RxBps: 2048, TxBps: 512,
			WiFi: probe.WiFiInfo{Present: true, QualityPct: 74, SignalDBm: -58}},
		{Name: "eth0", IsUp: true},
	}}
	m.iwLinks = map[string]probe.IWLink{"wlan0": {Present: true, SSID: "Hystax", FreqMHz: 5200, Channel: 40, TxMbps: 433}}
	m.pings = []probe.PingResult{{Target: "gateway", Addr: "192.168.88.1", OK: true, Method: "icmp"}}
	m.lossWin = map[string][]bool{"gateway": {true, true, false}}
	m.gpuSnap = probe.GPUInfo{Available: true, CoreTempC: 58, CoreVolts: 0.8625, MemARMMB: 948, MemGPUMB: 76,
		Clocks: []probe.ClockReading{{Name: "arm", MHz: 1500}, {Name: "core", MHz: 500}}}
	m.backlight = probe.BacklightInfo{Present: true, Device: "backlight", Brightness: 128, Max: 255, Percent: 50, Writable: true}
	m.procs = []probe.ProcInfo{
		{PID: 1, PPID: 0, Name: "systemd", User: "root", Status: "sleep", CPUPct: 0.1, MemPct: 0.4, MemRSS: 1 << 23},
		{PID: 420, PPID: 1, Name: "org.gnome.Shell.Extensions.Foo", User: "vlad", Status: "running", CPUPct: 12.5, MemRSS: 1 << 27},
	}
	m.conns = []probe.ConnInfo{
		{Proto: "TCP", LocalAddr: "0.0.0.0:22", RemoteAddr: "-", Status: "LISTEN", PID: 1, Process: "sshd"},
		{Proto: "TCP6", LocalAddr: "[::1]:631", RemoteAddr: "-", Status: "LISTEN", PID: 2, Process: "cupsd"},
	}
	m.alerts.update(map[string]alertCond{"temp:cpu_thermal": {sevCrit, "cpu_thermal 85°C ≥ 80°C critical"}}, nonZeroTime())

	m.refreshProcs()
	m.refreshAlerts()
	m.netHeader = m.buildNetHeader()
	m.netText = m.renderNetText()
	m.netVP.SetContent(hardClipLinesToWidth(m.netText, m.netVP.Width))
	return m
}

// renderSafely reports the frame's line count, or the panic message.
func renderSafely(m Model) (lines int, panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			panicMsg = fmt.Sprint(r)
		}
	}()
	out := m.View()
	if out == "" {
		return 0, ""
	}
	return len(strings.Split(out, "\n")), ""
}

// No terminal size may panic, and the frame may never be taller than the
// terminal — an overflowing frame makes Bubble Tea's alt-screen renderer blank
// the display, which is unrecoverable without a resize.
func TestFrameNeverPanicsOrOverflows(t *testing.T) {
	for _, w := range []int{0, 1, 2, 5, 6, 10, 20, 29, 30, 31, 40, 60, 80, 120, 213, 300} {
		for _, h := range []int{0, 1, 2, 4, 5, 6, 7, 10, 24, 30, 40} {
			m := geometryFixture(t, w, h)
			for tb := tab(0); tb < numTabs; tb++ {
				m.activeTab = tb
				n, p := renderSafely(m)
				if p != "" {
					t.Errorf("%dx%d tab %d: PANIC %s", w, h, tb, p)
					continue
				}
				if h > 0 && n > h {
					t.Errorf("%dx%d tab %d: frame is %d lines, terminal has %d rows", w, h, tb, n, h)
				}
			}
		}
	}
}

// The help and process-detail overlays share the frame budget.
func TestOverlaysNeverOverflow(t *testing.T) {
	for _, h := range []int{0, 1, 5, 6, 8, 12, 40} {
		for _, w := range []int{0, 20, 30, 60, 120} {
			m := geometryFixture(t, w, h)
			m.activeTab = tabProc

			m.showHelp = true
			if n, p := renderSafely(m); p != "" {
				t.Errorf("help %dx%d: PANIC %s", w, h, p)
			} else if h > 0 && n > h {
				t.Errorf("help %dx%d: %d lines > %d rows", w, h, n, h)
			}
			m.showHelp = false

			m.detail = &procDetailState{pid: 420, name: "org.gnome.Shell.Extensions.Foo", loaded: true,
				d: probe.ProcDetail{PID: 420, Name: "shell", User: "vlad", Status: "running",
					CmdLine: strings.Repeat("--flag=value ", 30), Exe: "/usr/bin/gnome-shell",
					Nice: 0, Threads: 12, FDs: 64, MemRSS: 1 << 27}}
			if n, p := renderSafely(m); p != "" {
				t.Errorf("detail %dx%d: PANIC %s", w, h, p)
			} else if h > 0 && n > h {
				t.Errorf("detail %dx%d: %d lines > %d rows", w, h, n, h)
			}
			m.detail = nil
		}
	}
}

// Below the minimum, ucmon says so on one line instead of drawing a broken box.
func TestTooSmallNotice(t *testing.T) {
	m := geometryFixture(t, minWidth-1, minHeight)
	out := m.View()
	if lines := strings.Split(out, "\n"); len(lines) != 1 {
		t.Errorf("too-small view is %d lines, want 1", len(lines))
	}
	if !strings.Contains(out, "need") {
		t.Errorf("too-small view lacks the requirement: %q", out)
	}

	// At exactly the minimum a real frame is drawn.
	m = geometryFixture(t, minWidth, minHeight)
	if out := m.View(); !strings.Contains(out, "ucmon") {
		t.Errorf("no frame at the minimum size %dx%d: %q", minWidth, minHeight, out)
	}

	// Before the first WindowSizeMsg the size is 0x0 and nothing is drawn.
	if out := NewModel(config.Default()).View(); out != "" {
		t.Errorf("unsized model rendered %q, want empty", out)
	}
}

// nonZeroTime is any non-zero instant; the views use TakenAt.IsZero() to tell
// "collecting…" from real data.
func nonZeroTime() time.Time { return time.Unix(1_700_000_000, 0) }

// trailingBlankRows counts empty content rows at the bottom of a rendered box,
// ignoring the border rows.
func trailingBlankRows(boxed string) int {
	lines := strings.Split(boxed, "\n")
	n := 0
	for i := len(lines) - 2; i > 0; i-- { // skip the bottom border
		if strings.TrimSpace(strings.ReplaceAll(lines[i], "│", "")) != "" {
			break
		}
		n++
	}
	return n
}

// The Network tab budgeted for a box border that bodyHeight already excludes,
// leaving dead rows at the bottom of the connection table. The Processes tab
// accounts exactly; Network must too, across the states that change the
// height of the block above the viewport.
func TestNetViewportFillsTheBox(t *testing.T) {
	base := geometryFixture(t, 120, 40)
	for i := 0; i < 60; i++ {
		base.conns = append(base.conns, probe.ConnInfo{
			Proto: "TCP", LocalAddr: fmt.Sprintf("10.0.0.%d:443", i),
			RemoteAddr: "-", Status: "ESTABLISHED", PID: int32(i), Process: "curl"})
	}

	cases := []struct {
		name  string
		setup func(m *Model)
	}{
		{"no selection", func(m *Model) {}},
		{"iface selected", func(m *Model) { m.netIfaceName = "eth0" }},
		{"wifi selected", func(m *Model) { m.netIfaceName = "wlan0" }},
		{"no ping", func(m *Model) { m.latSampler = nil; m.pings = nil }},
		{"collecting", func(m *Model) { m.netSnap = probe.NetSnapshot{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := base
			c.setup(&m)
			m.activeTab = tabNet
			m.netVP.Height = m.calcNetVPHeight()
			m.netText = m.renderNetText()
			m.netVP.SetContent(hardClipLinesToWidth(m.netText, m.netVP.Width))

			if n := trailingBlankRows(m.viewNet()); n != 0 {
				t.Errorf("%d dead rows at the bottom of the Network box", n)
			}
			// And the frame must still fit.
			if lines, p := renderSafely(m); p != "" {
				t.Errorf("PANIC %s", p)
			} else if lines > m.h {
				t.Errorf("frame is %d lines, terminal has %d rows", lines, m.h)
			}
		})
	}
}

// The Processes tab is the reference: it has always accounted exactly.
func TestProcViewportFillsTheBox(t *testing.T) {
	m := geometryFixture(t, 120, 40)
	for i := 0; i < 60; i++ {
		m.procs = append(m.procs, probe.ProcInfo{PID: int32(i + 100), Name: "worker", Status: "sleep"})
	}
	m.activeTab = tabProc
	m.refreshProcs()
	if n := trailingBlankRows(m.viewProcs()); n != 0 {
		t.Errorf("%d dead rows at the bottom of the Processes box", n)
	}
}

// The header carries optional badges (paused, sampling-lateness) that widen
// its left side. Over-wide content makes lipgloss wrap the header onto a
// second row, which pushes the whole frame past the terminal — the same
// overflow the box clipping fixes, arriving by a different route.
func TestHeaderNeverWrapsWithBadges(t *testing.T) {
	for _, w := range []int{0, 1, 10, 20, 30, 40, 60, 80, 120, 213} {
		for _, state := range []struct {
			name  string
			setup func(*Model)
		}{
			{"plain", func(m *Model) {}},
			{"paused", func(m *Model) { m.paused = true }},
			{"one probe late", func(m *Model) {
				m.inFlight[fetchSys] = true
				m.queue(nil, fetchSys, m.fetchSysCmd)
			}},
			{"paused and all late", func(m *Model) {
				m.paused = true
				for k := fetchKind(0); k < numFetchKinds; k++ {
					m.inFlight[k] = true
					m.queue(nil, k, m.fetchSysCmd)
				}
			}},
		} {
			m := geometryFixture(t, w, 20)
			state.setup(&m)

			if n := len(strings.Split(m.renderHeader(), "\n")); n != 1 {
				t.Errorf("%dx20 %s: header is %d lines, want 1", w, state.name, n)
			}
			n, p := renderSafely(m)
			if p != "" {
				t.Errorf("%dx20 %s: PANIC %s", w, state.name, p)
			} else if n > 20 {
				t.Errorf("%dx20 %s: frame is %d lines, terminal has 20", w, state.name, n)
			}
		}
	}
}
