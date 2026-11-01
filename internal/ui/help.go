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

// helpContent renders the key reference as one column.
func helpContent() string {
	return strings.Join(helpBlocks(0), "\n")
}

// helpBlocks renders each section as a block of lines. The key column is as
// wide as the section's longest key, and with width > 0 a description that
// would run past it wraps onto indented continuation lines — truncating it
// lost the end of "pause sampling (alerts keep running)".
func helpBlocks(width int) []string {
	blocks := make([]string, 0, len(helpSections))
	for _, sec := range helpSections {
		keyW := 0
		for _, k := range sec.keys {
			keyW = max(keyW, lipgloss.Width(k[0]))
		}
		var b strings.Builder
		b.WriteString(titleStyle.Render(sec.title) + "\n")
		for _, k := range sec.keys {
			lead := "  " + padRight(k[0], keyW) + "  "
			descW := 0
			if width > 0 {
				descW = max(12, width-lipgloss.Width(lead))
			}
			for i, part := range wrapWords(k[1], descW) {
				if i == 0 {
					b.WriteString("  " + okStyle.Render(padRight(k[0], keyW)) + "  " + subtleStyle.Render(part) + "\n")
				} else {
					b.WriteString(strings.Repeat(" ", lipgloss.Width(lead)) + subtleStyle.Render(part) + "\n")
				}
			}
		}
		blocks = append(blocks, b.String())
	}
	return blocks
}

// wrapWords breaks s into lines of at most width runes at spaces (width 0:
// one line).
func wrapWords(s string, width int) []string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return []string{s}
	}
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case lipgloss.Width(line)+1+lipgloss.Width(w) <= width:
			line += " " + w
		default:
			out = append(out, line)
			line = w
		}
	}
	return append(out, line)
}

func (m Model) viewHelp() string {
	w := m.boxWidth()
	bodyH := m.bodyHeight()

	head := titleStyle.Render("Help — key bindings") + "  " +
		subtleStyle.Render("(esc or ? to close)")
	content := head + "\n\n" + helpContent()

	// Two-column layout when the terminal is wide but short (uConsole),
	// split between sections so none is broken across the columns.
	if strings.Count(content, "\n") > bodyH && w >= 100 {
		colW := (w-2)/2 - 1
		content = head + "\n\n" + twoColumns(helpBlocks(colW), colW)
	}

	return m.box(content)
}

// twoColumns places whole blocks side by side, splitting the list where the
// taller column is shortest.
func twoColumns(blocks []string, colW int) string {
	split := func(k int) (left, right []string) {
		for i, bl := range blocks {
			lines := strings.Split(strings.TrimRight(bl, "\n"), "\n")
			col := &right
			if i < k {
				col = &left
			}
			if len(*col) > 0 {
				*col = append(*col, "")
			}
			*col = append(*col, lines...)
		}
		return left, right
	}
	best := 1
	for k := 1; k < len(blocks); k++ {
		l, r := split(k)
		bl, br := split(best)
		if max(len(l), len(r)) < max(len(bl), len(br)) {
			best = k
		}
	}
	left, right := split(best)

	var b strings.Builder
	for i := range max(len(left), len(right)) {
		l := ""
		if i < len(left) {
			l = ansiSafeTruncate(left[i], colW)
		}
		b.WriteString(l + strings.Repeat(" ", max(0, colW-lipgloss.Width(l))+2))
		if i < len(right) {
			b.WriteString(ansiSafeTruncate(right[i], colW))
		}
		b.WriteString("\n")
	}
	return b.String()
}
