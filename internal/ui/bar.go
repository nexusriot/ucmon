package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// RenderBar draws a horizontal bar chart like s3duck-tui's usage graph.
// width is the total character width for the bar (excluding label).
func RenderBar(pct float64, width int, color string) string {
	if width <= 0 {
		return ""
	}
	filled := int(pct / 100.0 * float64(width))
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	empty := width - filled

	style := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	bar := style.Render(strings.Repeat("█", filled)) + dimStyle.Render(strings.Repeat("░", empty))
	return bar
}

// RenderBarWithLabel draws a bar with percentage label.
func RenderBarWithLabel(label string, pct float64, width int) string {
	color := "42" // green
	switch {
	case pct >= 90:
		color = "196" // red
	case pct >= 75:
		color = "214" // orange
	case pct >= 50:
		color = "226" // yellow
	}

	barW := width - 8 // room for " XX.X%"
	if barW < 5 {
		barW = 5
	}

	return fmt.Sprintf("%s %5.1f%%", RenderBar(pct, barW, color), pct)
}
