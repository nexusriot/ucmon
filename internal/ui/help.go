package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// helpSections lists every key binding, grouped the way the tabs are.
// Kept as data so the help view and tests share one source of truth.
var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Global", [][2]string{
		{"tab / → , shift+tab / ←", "next / previous tab"},
		{"1–8", "jump to tab"},
		{"space", "pause sampling (alerts keep running)"},
		{"z", "expand a metric to a full-screen graph"},
		{"Z", "reset session peaks"},
		{"w", "write snapshot report"},
		{"?", "toggle this help"},
		{"ctrl+c", "quit"},
	}},
	{"Expanded graph (z)", [][2]string{
		{"← → / j k", "previous / next metric"},
		{"z / esc / q", "close"},
	}},
	{"CPU tab", [][2]string{
		{"g", "cycle cpufreq governor"},
	}},
	{"Processes tab", [][2]string{
		{"↑ ↓ pgup pgdn home end", "move cursor"},
		{"s", "cycle sort (CPU/MEM/I-O/PID/NAME)"},
		{"t", "toggle tree view"},
		{"enter", "open process details"},
		{"k / K", "SIGTERM / SIGKILL (y confirms)"},
		{"/ then enter", "filter (name, pid, user, cmdline)"},
		{"ctrl+u", "clear filter"},
	}},
	{"Process details", [][2]string{
		{"+ / -", "renice ±1"},
		{"p / c", "SIGSTOP / SIGCONT"},
		{"k / K", "SIGTERM / SIGKILL"},
		{"esc / q / enter", "close"},
	}},
	{"Network tab", [][2]string{
		{"j / k", "select interface"},
		{"f", "cycle connection state filter"},
		{"/", "filter connections"},
	}},
	{"GPU tab", [][2]string{
		{"+ / -", "backlight brightness"},
	}},
	{"Alerts tab", [][2]string{
		{"m", "mute / unmute banner"},
		{"c", "clear alert log"},
	}},
}

// helpContent renders the key reference as plain multi-column text.
func helpContent() string {
	var b strings.Builder
	for i, sec := range helpSections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(titleStyle.Render(sec.title) + "\n")
		for _, k := range sec.keys {
			b.WriteString("  " + okStyle.Render(padRight(k[0], 26)) + subtleStyle.Render(k[1]) + "\n")
		}
	}
	return b.String()
}

func (m Model) viewHelp() string {
	w := m.boxWidth()
	bodyH := m.bodyHeight()

	head := titleStyle.Render("Help — key bindings") + "  " +
		subtleStyle.Render("(esc or ? to close)")
	content := head + "\n\n" + helpContent()

	// Two-column layout when the terminal is wide but short (uConsole).
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > bodyH && w >= 100 {
		content = twoColumns(lines, bodyH, w/2-2)
	}

	return m.box(content)
}

// twoColumns lays lines out side by side when they exceed the height budget.
func twoColumns(lines []string, maxH, colW int) string {
	split := (len(lines) + 1) / 2
	if split > maxH {
		split = maxH
	}
	left, right := lines[:split], lines[split:]

	var b strings.Builder
	for i := 0; i < split; i++ {
		l := ansiSafeTruncate(left[i], colW)
		pad := colW - lipgloss.Width(l)
		if pad < 0 {
			pad = 0
		}
		b.WriteString(l + strings.Repeat(" ", pad+2))
		if i < len(right) {
			b.WriteString(ansiSafeTruncate(right[i], colW))
		}
		b.WriteString("\n")
	}
	return b.String()
}
