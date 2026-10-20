package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
)

func writeTOML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckConfigReportsUnknownKeys(t *testing.T) {
	path := writeTOML(t, "start_tab = \"net\"\ntemp_warnn = 90\nping_targets = \"1.1.1.1\"\n")

	out, ok := CheckConfig(path)
	if ok {
		t.Error("a file with typos should not report clean")
	}
	for _, want := range []string{"2 unrecognised keys", "line 2", "temp_warnn", "line 3", "ping_targets", "never fatal"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q\n%s", want, out)
		}
	}
}

func TestCheckConfigCleanAndMissingFiles(t *testing.T) {
	clean, ok := CheckConfig(writeTOML(t, "start_tab = \"cpu\"\n"))
	if !ok || !strings.Contains(clean, "no unrecognised keys") {
		t.Errorf("clean file: ok=%v\n%s", ok, clean)
	}

	// A missing config is the normal case — ucmon needs none — so it is not
	// a failure the exit status should report.
	missing := filepath.Join(t.TempDir(), "nope.toml")
	out, ok := CheckConfig(missing)
	if !ok || !strings.Contains(out, "not present") {
		t.Errorf("missing file: ok=%v\n%s", ok, out)
	}

	if out, ok := CheckConfig(""); !ok || !strings.Contains(out, "defaults") {
		t.Errorf("empty path: ok=%v\n%s", ok, out)
	}
}

func TestUnknownKeysNotice(t *testing.T) {
	if n := unknownKeysNotice(config.Default()); n != "" {
		t.Errorf("a clean config should produce no notice, got %q", n)
	}

	cfg := config.Default()
	cfg.Unknown = []config.UnknownKey{{Key: "temp_warnn", Line: 1}, {Key: "ping_targets", Line: 2}}
	n := unknownKeysNotice(cfg)
	if !strings.Contains(n, "temp_warnn") || !strings.Contains(n, "ping_targets") ||
		!strings.Contains(n, "--check-config") {
		t.Errorf("notice = %q", n)
	}

	// A config full of typos must still fit on one line.
	cfg.Unknown = append(cfg.Unknown,
		config.UnknownKey{Key: "a", Line: 3},
		config.UnknownKey{Key: "b", Line: 4},
		config.UnknownKey{Key: "c", Line: 5})
	n = unknownKeysNotice(cfg)
	if !strings.Contains(n, "+2 more") || strings.Contains(n, "\n") {
		t.Errorf("notice = %q", n)
	}
	if strings.Contains(n, ", c") {
		t.Errorf("notice should list only the first few keys: %q", n)
	}
}

// The notice has to reach the screen, not just exist: the TUI shows it in the
// footer for the first seconds after startup.
func TestStartupNoticeNamesUnknownKeys(t *testing.T) {
	cfg := config.Default()
	cfg.Unknown = []config.UnknownKey{{Key: "temp_warnn", Line: 4}}

	m := NewModel(cfg)
	if n := m.notice.get(); !strings.Contains(n, "temp_warnn") {
		t.Fatalf("startup notice = %q", n)
	}
	if n := NewModel(config.Default()).notice.get(); n != "" {
		t.Errorf("a clean config must start without a notice, got %q", n)
	}

	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if view := mm.(Model).View(); !strings.Contains(view, "temp_warnn") {
		t.Error("the startup notice never reached the footer")
	}
}
