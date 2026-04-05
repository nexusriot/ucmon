package ui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	subtleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	boxStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	hlStyle       = lipgloss.NewStyle().Reverse(true)
)
