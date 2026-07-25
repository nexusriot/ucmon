package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/ui"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	once := flag.Bool("once", false, "print one plain-text snapshot report to stdout and exit (no TUI)")
	configPath := flag.String("config", "", "config file path (default: "+config.Path()+")")
	startTab := flag.String("tab", "", "start tab: cpu|mem|proc|disk|net|power|gpu|alerts")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"ucmon %s — TUI system monitor for the uConsole / Raspberry Pi\n\nusage: ucmon [flags]\n\n",
			ui.Version())
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("ucmon " + ui.Version())
		return
	}

	var cfg config.Config
	if *configPath != "" {
		cfg = config.LoadFrom(*configPath)
	} else {
		cfg = config.Load()
	}
	if *startTab != "" {
		cfg.StartTab = *startTab
	}

	if *once {
		report, err := ui.RunOnce(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(report)
		return
	}

	p := tea.NewProgram(
		ui.NewModel(cfg),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
		os.Exit(1)
	}
}
