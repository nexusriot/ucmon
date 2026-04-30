package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/ui"
)

func main() {
	p := tea.NewProgram(
		ui.NewModel(),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
		os.Exit(1)
	}
}
