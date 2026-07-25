package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ucmon/internal/config"
)

// RunOnce is the --once code path: one real sweep over the live system.
// Ping is disabled so the test generates no network traffic.
func TestRunOnceSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("samples the live system with a 600ms settle window")
	}

	cfg := config.Default()
	cfg.PingEnabled = false

	report, err := RunOnce(cfg)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	for _, section := range []string{"ucmon " + version, "CPU", "Memory", "Power", "Disks", "Top processes", "Session peaks", "Alerts"} {
		if !strings.Contains(report, section) {
			t.Errorf("report missing %q", section)
		}
	}
	if strings.Contains(report, "Latency") {
		t.Error("latency section present despite ping_enabled = false")
	}
	if strings.Contains(report, "\x1b[") {
		t.Error("report contains ANSI escapes")
	}
}
