package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/ui"
)

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	once := flag.Bool("once", false, "print one plain-text snapshot report to stdout and exit (no TUI)")
	jsonOut := flag.Bool("json", false, "with --once, print the snapshot as JSON instead of text")
	metrics := flag.String("metrics", "", "serve Prometheus metrics on this address (e.g. :9100) and exit on signal (no TUI)")
	configPath := flag.String("config", "", "config file path (default: "+config.Path()+")")
	startTab := flag.String("tab", "", "start tab: cpu|mem|proc|disk|net|power|gpu|alerts")
	doctor := flag.Bool("doctor", false, "report which readings this host can produce, and why the rest are missing (no TUI)")
	checkConfig := flag.Bool("check-config", false, "list config keys ucmon does not recognise and exit (no TUI)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"ucmon %s — TUI system monitor for the uConsole / Raspberry Pi\n\nusage: ucmon [flags] [doctor]\n\n",
			ui.Version())
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println("ucmon " + ui.Version())
		return
	}

	// The report is what people will ask for by name, so accept it as a
	// subcommand as well as a flag. flag.Parse stops at the first non-flag
	// argument, so `ucmon doctor` and `ucmon --doctor` are the same request.
	if flag.Arg(0) == "doctor" {
		*doctor = true
	}

	path := *configPath
	if path == "" {
		path = config.Path()
	}

	if *checkConfig {
		report, ok := ui.CheckConfig(path)
		fmt.Print(report)
		if !ok {
			os.Exit(1)
		}
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

	// Diagnostics, not monitoring: no TUI, no exporter, and exit 0 whatever
	// the host turns out to be missing.
	if *doctor {
		fmt.Print(ui.Doctor(cfg, path))
		return
	}

	if *once {
		report, err := snapshot(cfg, *jsonOut)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(report)
		return
	}

	// --metrics wins over the config key, so a one-off scrape endpoint does
	// not need the file edited.
	addr := ui.ListenAddr(*metrics)
	if addr == "" {
		addr = ui.ListenAddr(cfg.MetricsListen)
	}
	if addr != "" && *metrics != "" {
		if err := serveMetrics(cfg, addr); err != nil {
			fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := runTUI(cfg, addr); err != nil {
		fmt.Fprintf(os.Stderr, "ucmon: %v\n", err)
		os.Exit(1)
	}
}

func snapshot(cfg config.Config, asJSON bool) (string, error) {
	if asJSON {
		return ui.RunOnceJSON(cfg)
	}
	return ui.RunOnce(cfg)
}

// serveMetrics runs the exporter in the foreground until the process is
// signalled, for use under systemd or in a container.
func serveMetrics(cfg config.Config, addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "ucmon %s: serving metrics on %s\n", ui.Version(), addr)
	return ui.Serve(ctx, cfg, addr)
}

// runTUI starts the monitor, optionally alongside the metrics exporter when
// the config asked for one. The exporter samples independently of the TUI, so
// a scrape never perturbs what is on screen.
func runTUI(cfg config.Config, metricsAddr string) error {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	if metricsAddr != "" {
		go func() {
			if err := ui.Serve(ctx, cfg, metricsAddr); err != nil {
				// The monitor is the point; a failed exporter must not take
				// it down, and stderr is behind the alt screen, so the
				// message waits there until the TUI exits.
				fmt.Fprintf(os.Stderr, "ucmon: metrics exporter: %v\n", err)
			}
		}()
	}

	model := ui.NewModel(cfg)
	model.Restore()

	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	final, err := p.Run()
	if err != nil {
		return err
	}
	if m, ok := final.(ui.Model); ok {
		if err := m.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "ucmon: could not save history: %v\n", err)
		}
	}
	return nil
}
