package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
)

func alertAt(sev alertSeverity, msg string, cleared bool) alertEntry {
	return alertEntry{when: time.Unix(1_700_000_000, 0), sev: sev, msg: msg, cleared: cleared}
}

func TestAppendAlertLogUnwritablePath(t *testing.T) {
	// A directory can never be opened for append.
	if err := appendAlertLog(t.TempDir(), []alertEntry{alertAt(sevWarn, "x", false)}); err == nil {
		t.Error("appending to a directory should fail")
	}
}

// The hook really runs under /bin/sh and really receives the UCMON_ALERT_*
// variables. runAlertCmd is synchronous; only its caller backgrounds it.
func TestRunAlertCmdPassesEnvironment(t *testing.T) {
	out := filepath.Join(t.TempDir(), "hook.out")
	runAlertCmd("printf '%s|%s|%s' \"$UCMON_ALERT_STATE\" \"$UCMON_ALERT_SEV\" \"$UCMON_ALERT_MSG\" > "+out,
		alertAt(sevCrit, "battery 3% <= 5% critical", false))

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	if got := string(b); got != "raise|CRIT|battery 3% <= 5% critical" {
		t.Errorf("hook saw %q", got)
	}
}

// A failing hook is swallowed by design (the alert itself is already visible),
// so it must not panic or block.
func TestRunAlertCmdSurvivesFailure(t *testing.T) {
	runAlertCmd("exit 7", alertAt(sevWarn, "x", false))
	runAlertCmd("ucmon-no-such-command-exists", alertAt(sevWarn, "x", false))
}

// handleAlertEvents wires the log file up end to end.
func TestHandleAlertEventsWritesLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.log")
	cfg := config.Default()
	cfg.AlertLogFile = path
	cfg.AlertBell = false
	m := NewModel(cfg)

	m.handleAlertEvents([]alertEntry{
		alertAt(sevCrit, "under-voltage detected", false),
		alertAt(sevWarn, "load 8.10 >= 8.0", false),
		alertAt(sevCrit, "under-voltage detected", true),
	})

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no log written: %v", err)
	}
	if !strings.Contains(string(b), "under-voltage") || !strings.Contains(string(b), "load 8.10") {
		t.Errorf("log missing events:\n%s", b)
	}
	// Resolving a condition is logged too, or the file only ever shows what
	// broke and never that it recovered.
	if !strings.Contains(string(b), "CLEAR") {
		t.Errorf("the cleared event was not logged:\n%s", b)
	}
	if n := m.notice.get(); n != "" {
		t.Errorf("a successful write set an error notice: %q", n)
	}
}

// A log path that cannot be written surfaces as a footer notice rather than
// failing silently or killing the tick.
func TestHandleAlertEventsReportsLogFailure(t *testing.T) {
	cfg := config.Default()
	cfg.AlertLogFile = t.TempDir() // a directory
	cfg.AlertBell = false
	m := NewModel(cfg)

	m.handleAlertEvents([]alertEntry{alertAt(sevCrit, "boom", false)})

	if n := m.notice.get(); !strings.Contains(n, "alert log") {
		t.Errorf("notice = %q, want it to mention the alert log", n)
	}
}

// No events means no side effects at all — not even creating the log file.
func TestHandleAlertEventsNoopOnEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "alerts.log")
	cfg := config.Default()
	cfg.AlertLogFile = path
	m := NewModel(cfg)

	m.handleAlertEvents(nil)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the log file was created for zero events (err=%v)", err)
	}
}

// The bell only fires for a fresh critical, and muting silences it. Writing to
// stderr is the side effect, so this asserts the surrounding wiring: a muted
// engine or a warn-only batch must still reach the log.
func TestHandleAlertEventsBellDoesNotBlockDelivery(t *testing.T) {
	for _, c := range []struct {
		name  string
		muted bool
		sev   alertSeverity
	}{
		{"crit unmuted", false, sevCrit},
		{"crit muted", true, sevCrit},
		{"warn unmuted", false, sevWarn},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "alerts.log")
			cfg := config.Default()
			cfg.AlertLogFile = path
			cfg.AlertBell = true
			m := NewModel(cfg)
			m.alerts.muted = c.muted

			m.handleAlertEvents([]alertEntry{alertAt(c.sev, "event", false)})

			if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "event") {
				t.Errorf("log not written (err=%v)", err)
			}
		})
	}
}
