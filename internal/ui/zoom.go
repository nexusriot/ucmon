package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// zoomState is the metric currently expanded to fill the frame.
type zoomState struct {
	idx int // index into the zoomable metrics of the tab it was opened from
	tab tab
}

// zoomMetric is one series a tab can expand. The ring buffers already hold
// the data; a one-row sparkline is simply the smallest view of it.
type zoomMetric struct {
	title string
	unit  string
	hist  []float64
	lo    float64 // axis floor
	hi    float64 // axis ceiling; 0 means scale to the data
	fixed bool    // true when lo/hi are a real axis rather than a hint
	color string
}

// zoomMetrics lists what the active tab can expand, in display order.
func (m Model) zoomMetrics() []zoomMetric {
	pct := func(title string, hist []float64, color string) zoomMetric {
		return zoomMetric{title: title, unit: "%", hist: hist, lo: 0, hi: 100, fixed: true, color: color}
	}

	switch m.activeTab {
	case tabCPU:
		out := []zoomMetric{pct("CPU total", m.cpuHist, "42")}
		for i, h := range m.coreHists {
			out = append(out, pct(fmt.Sprintf("Core %d", i), h, "39"))
		}
		for _, t := range m.cpuSnap.Temperatures {
			out = append(out, zoomMetric{
				title: "Temperature " + t.Label, unit: "°C",
				hist: m.tempHist[t.Label], color: "214",
			})
		}
		for _, f := range m.cpuSnap.Freqs {
			out = append(out, zoomMetric{
				title: "Frequency " + f.Policy, unit: " MHz",
				hist: m.freqHist[f.Policy], lo: f.MinMHz, hi: f.MaxMHz,
				fixed: f.MaxMHz > f.MinMHz, color: "45",
			})
		}
		return out

	case tabMem:
		out := []zoomMetric{pct("RAM used", m.memHist, "42")}
		if m.memSnap.SwapTotal > 0 {
			out = append(out, pct("Swap used", m.swapHist, "214"))
		}
		return out

	case tabNet:
		var out []zoomMetric
		for _, ii := range m.upIfaces() {
			out = append(out,
				zoomMetric{title: ii.Name + " RX", unit: " B/s", hist: m.rxHist[ii.Name], color: "42"},
				zoomMetric{title: ii.Name + " TX", unit: " B/s", hist: m.txHist[ii.Name], color: "45"})
		}
		for _, r := range m.pings {
			out = append(out, zoomMetric{
				title: "RTT " + r.Target, unit: " ms", hist: m.rttHist[r.Target], color: "226",
			})
		}
		return out

	case tabPower:
		out := []zoomMetric{{title: "Load (1 min)", hist: m.loadHist, color: "42"}}
		if m.powerSnap.Battery.Present {
			out = append(out, pct("Battery charge", m.battHist, "45"))
			if len(m.battWHist) > 1 {
				out = append(out, zoomMetric{
					title: "Battery power", unit: " W", hist: m.battWHist, color: "214",
				})
			}
		}
		return out
	}
	return nil
}

// toggleZoom opens or closes the expanded view for the active tab.
func (m *Model) toggleZoom() {
	if m.zoom != nil {
		m.zoom = nil
		return
	}
	if len(m.zoomMetrics()) == 0 {
		m.setNotice(subtleStyle.Render("nothing to expand on this tab"))
		return
	}
	m.zoom = &zoomState{tab: m.activeTab}
}

// cycleZoom steps to the next or previous metric, wrapping.
func (m *Model) cycleZoom(delta int) {
	if m.zoom == nil {
		return
	}
	n := len(m.zoomMetrics())
	if n == 0 {
		m.zoom = nil
		return
	}
	m.zoom.idx = ((m.zoom.idx+delta)%n + n) % n
}

func (m Model) viewZoom() string {
	metrics := m.zoomMetrics()
	if len(metrics) == 0 {
		return m.box(subtleStyle.Render("nothing to expand on this tab"))
	}
	idx := m.zoom.idx
	if idx >= len(metrics) {
		idx = len(metrics) - 1
	}
	z := metrics[idx]

	w := max(20, m.boxWidth()-2)
	bodyH := m.bodyHeight()

	var b strings.Builder
	b.WriteString(titleStyle.Render(z.title) + "  " +
		subtleStyle.Render(fmt.Sprintf("(%d/%d — ←/→ or j/k switch, z or esc close)", idx+1, len(metrics))) + "\n")

	lo, hi := graphBounds(z)
	cur, has := lastValue(z.hist)
	summary := subtleStyle.Render("no samples yet")
	if has {
		lv, hv, avg := seriesStats(z.hist)
		summary = fmt.Sprintf("%s %s   %s %s   %s %s   %s %s",
			subtleStyle.Render("now"), okStyle.Render(fmtMetric(cur, z.unit)),
			subtleStyle.Render("min"), fmtMetric(lv, z.unit),
			subtleStyle.Render("max"), fmtMetric(hv, z.unit),
			subtleStyle.Render("avg"), fmtMetric(avg, z.unit))
	}
	b.WriteString(summary + "\n\n")

	// Title, summary, the blank between them and the footer below the plot:
	// everything else in the frame belongs to the chart.
	const chrome = 4
	rows := max(2, bodyH-chrome)
	labelW := len(fmtMetric(hi, z.unit)) + 1
	if lw := len(fmtMetric(lo, z.unit)) + 1; lw > labelW {
		labelW = lw
	}
	plotW := max(8, w-labelW-1)

	b.WriteString(renderBraille(z.hist, plotW, rows, lo, hi, z.unit, labelW, z.color))
	b.WriteString("\n" + subtleStyle.Render(fmt.Sprintf(
		"%d samples shown of %d kept  •  oldest left, newest right",
		min(plotW*2, len(z.hist)), len(z.hist))))

	return m.box(b.String())
}

// graphBounds picks the plot's vertical axis: the declared one when the
// metric has a real ceiling, otherwise the data's own range padded a little
// so a flat line does not sit on the floor.
func graphBounds(z zoomMetric) (lo, hi float64) {
	if z.fixed && z.hi > z.lo {
		return z.lo, z.hi
	}
	if len(z.hist) == 0 {
		return 0, 1
	}
	lo, hi, _ = seriesStats(z.hist)
	if hi-lo < 1e-9 {
		// A constant series still deserves a readable axis around it.
		pad := math.Max(math.Abs(hi)*0.1, 1)
		return lo - pad, hi + pad
	}
	pad := (hi - lo) * 0.1
	lo -= pad
	hi += pad
	if z.lo == 0 && lo < 0 && !hasNegative(z.hist) {
		lo = 0 // never show a negative floor for a non-negative series
	}
	return lo, hi
}

func hasNegative(vs []float64) bool {
	for _, v := range vs {
		if v < 0 {
			return true
		}
	}
	return false
}

func seriesStats(vs []float64) (lo, hi, avg float64) {
	if len(vs) == 0 {
		return 0, 0, 0
	}
	lo, hi = vs[0], vs[0]
	sum := 0.0
	for _, v := range vs {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
		sum += v
	}
	return lo, hi, sum / float64(len(vs))
}

func lastValue(vs []float64) (float64, bool) {
	if len(vs) == 0 {
		return 0, false
	}
	return vs[len(vs)-1], true
}

func fmtMetric(v float64, unit string) string {
	switch unit {
	case " B/s":
		return humanRate(v)
	case "%", "°C":
		return fmt.Sprintf("%.1f%s", v, unit)
	case " MHz", " ms", " W":
		return fmt.Sprintf("%.1f%s", v, unit)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// humanRate is the byte-rate formatter, kept local so zoom does not depend on
// the probe package's display helpers for one call.
func humanRate(bps float64) string {
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	div, exp := unit, 0
	for n := bps / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", bps/div, []string{"KiB/s", "MiB/s", "GiB/s", "TiB/s", "PiB/s"}[exp])
}

// brailleDots maps a (column, row) position inside one braille cell to its
// bit in the U+2800 block. A cell is 2 columns wide and 4 rows tall, so a
// braille plot carries eight times the resolution of a block sparkline in the
// same character count.
var brailleDots = [2][4]rune{
	{0x01, 0x02, 0x04, 0x40}, // left column, top to bottom
	{0x08, 0x10, 0x20, 0x80}, // right column
}

// renderBraille plots values as a braille line chart rows tall and width
// cells wide, with a value axis down the left edge.
func renderBraille(values []float64, width, rows int, lo, hi float64, unit string, labelW int, color string) string {
	if width <= 0 || rows <= 0 {
		return ""
	}
	span := hi - lo
	if span <= 0 {
		span = 1
	}

	grid := make([][]rune, rows)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = 0
		}
	}

	// Each cell column holds two sample columns.
	cols := width * 2
	pts := values
	if len(pts) > cols {
		pts = pts[len(pts)-cols:]
	}
	// Right-align: the newest sample belongs at the right edge.
	offset := cols - len(pts)

	totalRows := rows * 4
	for i, v := range pts {
		x := offset + i
		norm := (v - lo) / span
		if norm < 0 {
			norm = 0
		}
		if norm > 1 {
			norm = 1
		}
		// Row 0 is the top of the plot, so a high value maps to a low index.
		y := int(math.Round((1 - norm) * float64(totalRows-1)))
		if y < 0 {
			y = 0
		}
		if y >= totalRows {
			y = totalRows - 1
		}
		grid[y/4][x/2] |= brailleDots[x%2][y%4]
	}

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	var b strings.Builder
	for r := 0; r < rows; r++ {
		// Axis label on the first, middle and last rows only; more would
		// crowd a 480-pixel-tall panel.
		label := ""
		switch r {
		case 0:
			label = fmtMetric(hi, unit)
		case rows - 1:
			label = fmtMetric(lo, unit)
		case rows / 2:
			label = fmtMetric(lo+span/2, unit)
		}
		b.WriteString(subtleStyle.Render(padTo(labelW, label)) + " ")

		var line strings.Builder
		for c := 0; c < width; c++ {
			if grid[r][c] == 0 {
				line.WriteRune(' ')
				continue
			}
			line.WriteRune(0x2800 | grid[r][c])
		}
		b.WriteString(style.Render(line.String()))
		if r < rows-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
