package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
)

func historyModel(t *testing.T, path string) Model {
	t.Helper()
	cfg := config.Default()
	cfg.HistoryFile = path
	return NewModel(cfg)
}

func TestHistoryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "history.json")

	m := historyModel(t, path)
	m.cpuHist = []float64{1, 2, 3}
	m.memHist = []float64{40, 41}
	m.coreHists = [][]float64{{5, 6}, {7, 8}}
	m.tempHist = map[string][]float64{"cpu_thermal": {50, 51}}
	m.rxHist = map[string][]float64{"wlan0": {100, 200}}
	m.peaks.cpuPct = 93.5
	m.peaks.tempC = 71.2
	m.peaks.tempLabel = "cpu_thermal"
	m.peaks.battMin = 22

	if err := m.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	restored := historyModel(t, path)
	restored.Restore()

	if !restored.restored {
		t.Fatal("Restore did not mark the history as loaded")
	}
	if got := restored.cpuHist; len(got) != 3 || got[2] != 3 {
		t.Errorf("cpuHist = %v", got)
	}
	if got := restored.coreHists; len(got) != 2 || got[1][1] != 8 {
		t.Errorf("coreHists = %v", got)
	}
	if got := restored.tempHist["cpu_thermal"]; len(got) != 2 || got[1] != 51 {
		t.Errorf("tempHist = %v", got)
	}
	if got := restored.rxHist["wlan0"]; len(got) != 2 || got[1] != 200 {
		t.Errorf("rxHist = %v", got)
	}
	if restored.peaks.cpuPct != 93.5 || restored.peaks.tempLabel != "cpu_thermal" {
		t.Errorf("peaks = %+v", restored.peaks)
	}
	if restored.peaks.battMin != 22 {
		t.Errorf("battMin = %d, want 22", restored.peaks.battMin)
	}
}

// Replaying yesterday's shape next to this minute's reading would be a lie
// told convincingly, so a stale file is ignored.
func TestHistoryIgnoresStaleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	stale := persistedHistory{
		Format:  historyFormat,
		SavedAt: time.Now().Add(-historyMaxAge - time.Hour),
		CPU:     []float64{99, 99, 99},
	}
	b, _ := json.Marshal(stale)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := historyModel(t, path)
	m.Restore()
	if len(m.cpuHist) != 0 {
		t.Errorf("a %v-old file was restored: %v", historyMaxAge, m.cpuHist)
	}
	if m.restored {
		t.Error("stale history marked as restored")
	}
}

func TestHistoryIgnoresForeignFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	b, _ := json.Marshal(persistedHistory{
		Format: historyFormat + 99, SavedAt: time.Now(), CPU: []float64{1, 2},
	})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	m := historyModel(t, path)
	m.Restore()
	if len(m.cpuHist) != 0 {
		t.Error("a file from a different format version was restored")
	}
}

func TestHistoryIgnoresGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := historyModel(t, path)
	m.Restore() // must not panic
	if len(m.cpuHist) != 0 {
		t.Error("garbage was restored")
	}
}

func TestHistoryDisabledByDefault(t *testing.T) {
	m := NewModel(config.Default())
	m.cpuHist = []float64{1, 2, 3}
	if err := m.Save(); err != nil {
		t.Errorf("Save with no history_file returned %v, want nil", err)
	}
	m.Restore() // no-op
	if len(m.cpuHist) != 3 {
		t.Error("Restore with no history_file disturbed the live history")
	}
}

func TestHistoryMissingFileIsNotAnError(t *testing.T) {
	m := historyModel(t, filepath.Join(t.TempDir(), "absent.json"))
	m.Restore()
	if m.restored {
		t.Error("a missing file was reported as restored")
	}
}

// A restored series must still respect the configured ring length, or an old
// file written with a longer history would silently raise the ceiling.
func TestHistoryClampsToConfiguredLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	long := make([]float64, 500)
	for i := range long {
		long[i] = float64(i)
	}
	b, _ := json.Marshal(persistedHistory{
		Format: historyFormat, SavedAt: time.Now(), CPU: long,
	})
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.HistoryFile = path
	cfg.HistoryLen = 50
	m := NewModel(cfg)
	m.Restore()

	if len(m.cpuHist) != 50 {
		t.Errorf("restored %d samples, want the configured 50", len(m.cpuHist))
	}
	// The tail is what matters: the newest samples must survive.
	if m.cpuHist[len(m.cpuHist)-1] != 499 {
		t.Errorf("last restored sample = %v, want 499", m.cpuHist[len(m.cpuHist)-1])
	}
}

// A half-written file would be silently discarded on the next start, which
// looks exactly like the history never being saved at all.
func TestHistoryWritesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.json")

	m := historyModel(t, path)
	m.cpuHist = []float64{1, 2, 3}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// A sparkline that starts out full of shape would read as live traffic that
// is not happening, so a restore announces itself.
func TestRestoreSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	m := historyModel(t, path)
	m.cpuHist = []float64{1, 2, 3}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	restored := historyModel(t, path)
	restored.Restore()
	if n := restored.notice.get(); n == "" {
		t.Error("a restored session said nothing about where its history came from")
	}

	// Nothing to restore, nothing to say.
	fresh := historyModel(t, filepath.Join(t.TempDir(), "absent.json"))
	fresh.Restore()
	if n := fresh.notice.get(); n != "" {
		t.Errorf("a fresh session announced a restore: %q", n)
	}
}
