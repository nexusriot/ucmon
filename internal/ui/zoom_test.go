package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func zoomModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(config.Default())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = m2.(Model)
	m.cpuHist = []float64{10, 20, 30, 40, 50, 60}
	m.coreHists = [][]float64{{1, 2, 3}, {4, 5, 6}}
	m.cpuSnap.Temperatures = []probe.TempReading{{Label: "cpu_thermal", Temp: 50}}
	m.tempHist = map[string][]float64{"cpu_thermal": {40, 45, 50}}
	return m
}

func TestZoomOpensAndCloses(t *testing.T) {
	m := zoomModel(t)

	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = m2.(Model)
	if m.zoom == nil {
		t.Fatal("z did not open the expanded view")
	}
	if !strings.Contains(m.View(), "CPU total") {
		t.Error("expanded view does not name the metric")
	}

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m2.(Model).zoom != nil {
		t.Error("esc did not close the expanded view")
	}
}

func TestZoomCyclesMetrics(t *testing.T) {
	m := zoomModel(t)
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = m2.(Model)

	metrics := m.zoomMetrics()
	if len(metrics) < 3 {
		t.Fatalf("CPU tab offers %d metrics, want total + cores + temp", len(metrics))
	}
	first := m.View()

	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = m2.(Model)
	if m.zoom.idx != 1 {
		t.Errorf("idx = %d after one step, want 1", m.zoom.idx)
	}
	if m.View() == first {
		t.Error("stepping to the next metric did not change the view")
	}

	// Wrapping backwards from the first metric lands on the last.
	m.zoom.idx = 0
	m2, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if got := m2.(Model).zoom.idx; got != len(metrics)-1 {
		t.Errorf("idx = %d after wrapping back, want %d", got, len(metrics)-1)
	}
}

// The expanded view is still a frame: it must obey the same height and width
// budget as every tab, or the alt screen blanks on a short display.
func TestZoomFitsTheFrame(t *testing.T) {
	for _, size := range []struct{ w, h int }{
		{120, 30}, {80, 24}, {160, 12}, {40, 8}, {30, 6},
	} {
		m := NewModel(config.Default())
		m2, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		m = m2.(Model)
		m.cpuHist = []float64{1, 50, 99, 20, 70}
		m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
		m = m3.(Model)

		view := m.View()
		lines := strings.Split(view, "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: expanded view is %d lines, budget %d", size.w, size.h, len(lines), size.h)
		}
		for i, ln := range lines {
			if w := lipgloss.Width(ln); w > size.w {
				t.Errorf("%dx%d: line %d is %d columns wide", size.w, size.h, i, w)
			}
		}
	}
}

func TestZoomOnATabWithNothingToShow(t *testing.T) {
	m := NewModel(config.Default())
	m2, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = m2.(Model)
	m.activeTab = tabAlerts

	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = m3.(Model)
	if m.zoom != nil {
		t.Error("z opened an expanded view on a tab with no series")
	}
	if n := m.notice.get(); n == "" {
		t.Error("z on a tab with no series said nothing")
	}
}

func TestRenderBrailleShape(t *testing.T) {
	out := renderBraille([]float64{0, 50, 100}, 10, 4, 0, 100, "%", 6, "42")
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d rows, want 4", len(lines))
	}
	// A value at the ceiling belongs on the top row, one at the floor on the
	// bottom; an inverted axis is the classic plotting mistake.
	if !strings.ContainsAny(lines[0], "⠁⠈⡀⠄⠂⠠⢀⣿⠉⠒⠤⣀") && !hasBraille(lines[0]) {
		t.Errorf("top row has no plotted point: %q", lines[0])
	}
	if !hasBraille(lines[len(lines)-1]) {
		t.Errorf("bottom row has no plotted point: %q", lines[len(lines)-1])
	}
}

func TestRenderBrailleRejectsDegenerateSize(t *testing.T) {
	if got := renderBraille([]float64{1}, 0, 4, 0, 1, "%", 2, "42"); got != "" {
		t.Errorf("zero width produced %q", got)
	}
	if got := renderBraille([]float64{1}, 4, 0, 0, 1, "%", 2, "42"); got != "" {
		t.Errorf("zero height produced %q", got)
	}
}

func TestGraphBoundsFixedAxis(t *testing.T) {
	z := zoomMetric{hist: []float64{40, 50}, lo: 0, hi: 100, fixed: true}
	if lo, hi := graphBounds(z); lo != 0 || hi != 100 {
		t.Errorf("fixed axis = %v..%v, want 0..100", lo, hi)
	}
}

func TestGraphBoundsPadsAutoAxis(t *testing.T) {
	z := zoomMetric{hist: []float64{10, 20, 30}}
	lo, hi := graphBounds(z)
	if lo > 10 || hi < 30 {
		t.Errorf("auto axis %v..%v does not contain the data", lo, hi)
	}
	// A non-negative series must never be given a negative floor.
	if lo < 0 {
		t.Errorf("auto axis floor = %v for a non-negative series", lo)
	}
}

func TestGraphBoundsConstantSeries(t *testing.T) {
	lo, hi := graphBounds(zoomMetric{hist: []float64{7, 7, 7}})
	if hi <= lo {
		t.Errorf("a constant series got a degenerate axis %v..%v", lo, hi)
	}
	if 7 < lo || 7 > hi {
		t.Errorf("axis %v..%v does not contain the constant value", lo, hi)
	}
}

func TestZoomSurvivesEmptyHistory(t *testing.T) {
	m := zoomModel(t)
	m.cpuHist = nil
	m.coreHists = nil
	m.tempHist = map[string][]float64{"cpu_thermal": nil}
	m2, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m = m2.(Model)
	if m.zoom == nil {
		t.Fatal("z did not open")
	}
	if !strings.Contains(m.View(), "no samples yet") {
		t.Error("an empty series did not say so")
	}
}

func hasBraille(s string) bool {
	for _, r := range s {
		if r >= 0x2800 && r <= 0x28FF && r != 0x2800 {
			return true
		}
	}
	return false
}

func TestFmtMetricPerUnit(t *testing.T) {
	cases := []struct {
		v    float64
		unit string
		want string
	}{
		{42.5, "%", "42.5%"},
		{61.26, "°C", "61.3°C"},
		{1800, " MHz", "1800.0 MHz"},
		{7.5, " ms", "7.5 ms"},
		{-3.25, " W", "-3.2 W"},
		{2.5, "", "2.50"},
		{512, " B/s", "512 B/s"},
		{1536, " B/s", "1.5 KiB/s"},
		{5 << 20, " B/s", "5.0 MiB/s"},
	}
	for _, c := range cases {
		if got := fmtMetric(c.v, c.unit); got != c.want {
			t.Errorf("fmtMetric(%v, %q) = %q, want %q", c.v, c.unit, got, c.want)
		}
	}
}

func TestHasNegative(t *testing.T) {
	if hasNegative([]float64{1, 2, 0}) {
		t.Error("a non-negative series reported a negative value")
	}
	// Battery power is signed: negative means discharging.
	if !hasNegative([]float64{1, -2, 3}) {
		t.Error("a signed series reported no negative value")
	}
	if hasNegative(nil) {
		t.Error("an empty series reported a negative value")
	}
}

// A signed series keeps its negative floor; the axis must not be clipped at 0.
func TestGraphBoundsKeepsNegativeFloorForSignedSeries(t *testing.T) {
	lo, _ := graphBounds(zoomMetric{hist: []float64{-5, -2, 3}})
	if lo >= 0 {
		t.Errorf("axis floor = %v for a series with negative samples", lo)
	}
}

func TestCycleZoomWithNoOpenOverlay(t *testing.T) {
	m := NewModel(config.Default())
	m.cycleZoom(1) // must not panic with m.zoom == nil
	if m.zoom != nil {
		t.Error("cycleZoom opened an overlay")
	}
}

// A metric list that empties under an open overlay closes it rather than
// indexing into nothing.
func TestCycleZoomClosesWhenMetricsVanish(t *testing.T) {
	m := zoomModel(t)
	m.zoom = &zoomState{tab: tabCPU}
	m.activeTab = tabAlerts // no zoomable series here
	m.cycleZoom(1)
	if m.zoom != nil {
		t.Error("cycleZoom kept an overlay open with no metrics to show")
	}
}

func TestZoomMetricsPerTab(t *testing.T) {
	m := zoomModel(t)
	m.memSnap.SwapTotal = 1 << 30
	m.swapHist = []float64{1, 2}
	m.netSnap.Ifaces = []probe.IfaceInfo{{Name: "wlan0", IsUp: true}}
	m.rxHist = map[string][]float64{"wlan0": {1, 2}}
	m.txHist = map[string][]float64{"wlan0": {3, 4}}
	m.powerSnap.Battery.Present = true
	m.battHist = []float64{90, 89}
	m.battWHist = []float64{-2, -3}

	for _, c := range []struct {
		tab  tab
		want []string
	}{
		{tabMem, []string{"RAM used", "Swap used"}},
		{tabNet, []string{"wlan0 RX", "wlan0 TX"}},
		{tabPower, []string{"Load (1 min)", "Battery charge", "Battery power"}},
	} {
		m.activeTab = c.tab
		var titles []string
		for _, z := range m.zoomMetrics() {
			titles = append(titles, z.title)
		}
		for _, want := range c.want {
			found := false
			for _, got := range titles {
				if got == want {
					found = true
				}
			}
			if !found {
				t.Errorf("tab %v offers %v, missing %q", c.tab, titles, want)
			}
		}
	}

	// A percentage series must be plotted on a fixed axis, not auto-scaled.
	m.activeTab = tabMem
	if z := m.zoomMetrics()[0]; !z.fixed || z.lo != 0 || z.hi != 100 {
		t.Errorf("RAM metric axis = %v..%v fixed=%v, want a fixed 0..100", z.lo, z.hi, z.fixed)
	}
	// A throughput series must not be.
	m.activeTab = tabNet
	if z := m.zoomMetrics()[0]; z.fixed {
		t.Errorf("a throughput metric was given a fixed axis: %+v", z)
	}
}
