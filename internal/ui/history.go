package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

// probeClamp is probe.ClampHistory under a shorter name, used often here.
func probeClamp(s []float64, max int) []float64 { return probe.ClampHistory(s, max) }

// historyFormat is bumped whenever the persisted shape changes, so an old
// file is ignored rather than decoded into the wrong fields.
const historyFormat = 1

// historyMaxAge drops a file older than this. Sparklines are a picture of the
// recent past; replaying yesterday's shape next to this minute's reading
// would be a lie told convincingly.
const historyMaxAge = 6 * time.Hour

// persistedHistory is the on-disk form of the sparkline ring buffers.
type persistedHistory struct {
	Format  int                  `json:"format"`
	SavedAt time.Time            `json:"saved_at"`
	Version string               `json:"version"`
	CPU     []float64            `json:"cpu,omitempty"`
	Mem     []float64            `json:"mem,omitempty"`
	Swap    []float64            `json:"swap,omitempty"`
	Load    []float64            `json:"load,omitempty"`
	Batt    []float64            `json:"batt,omitempty"`
	BattW   []float64            `json:"batt_w,omitempty"`
	Cores   [][]float64          `json:"cores,omitempty"`
	Temps   map[string][]float64 `json:"temps,omitempty"`
	Freqs   map[string][]float64 `json:"freqs,omitempty"`
	Rx      map[string][]float64 `json:"rx,omitempty"`
	Tx      map[string][]float64 `json:"tx,omitempty"`
	RTT     map[string][]float64 `json:"rtt,omitempty"`
	Peaks   persistedPeaks       `json:"peaks"`
}

type persistedPeaks struct {
	CPUPct    float64 `json:"cpu_pct"`
	TempC     float64 `json:"temp_c"`
	TempLabel string  `json:"temp_label"`
	Load1     float64 `json:"load1"`
	MemPct    float64 `json:"mem_pct"`
	BattMin   int     `json:"batt_min"`
}

// saveHistory writes the ring buffers so sparklines survive a restart.
// Failures are returned but never fatal: losing a picture of the last three
// minutes is not worth refusing to exit over.
func (m Model) saveHistory() error {
	path := m.cfg.HistoryFile
	if path == "" {
		return nil
	}

	h := persistedHistory{
		Format:  historyFormat,
		SavedAt: time.Now(),
		Version: version,
		CPU:     m.cpuHist,
		Mem:     m.memHist,
		Swap:    m.swapHist,
		Load:    m.loadHist,
		Batt:    m.battHist,
		BattW:   m.battWHist,
		Cores:   m.coreHists,
		Temps:   m.tempHist,
		Freqs:   m.freqHist,
		Rx:      m.rxHist,
		Tx:      m.txHist,
		RTT:     m.rttHist,
		Peaks: persistedPeaks{
			CPUPct:    m.peaks.cpuPct,
			TempC:     m.peaks.tempC,
			TempLabel: m.peaks.tempLabel,
			Load1:     m.peaks.load1,
			MemPct:    m.peaks.memPct,
			BattMin:   m.peaks.battMin,
		},
	}

	b, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	// Write-then-rename: a half-written file would be silently discarded on
	// the next start, which looks exactly like the history never being saved.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadHistory restores the ring buffers from a previous session. A missing,
// stale, malformed or foreign-format file is simply ignored.
func (m *Model) loadHistory() {
	path := m.cfg.HistoryFile
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var h persistedHistory
	if err := json.Unmarshal(b, &h); err != nil || h.Format != historyFormat {
		return
	}
	if h.SavedAt.IsZero() || time.Since(h.SavedAt) > historyMaxAge {
		return
	}

	limit := m.histLen()
	clampSlice := func(s []float64) []float64 { return probeClamp(s, limit) }
	clampMap := func(in map[string][]float64) map[string][]float64 {
		out := make(map[string][]float64, len(in))
		for k, v := range in {
			if len(v) > 0 {
				out[k] = probeClamp(v, limit)
			}
		}
		return out
	}

	m.cpuHist = clampSlice(h.CPU)
	m.memHist = clampSlice(h.Mem)
	m.swapHist = clampSlice(h.Swap)
	m.loadHist = clampSlice(h.Load)
	m.battHist = clampSlice(h.Batt)
	m.battWHist = clampSlice(h.BattW)

	m.coreHists = nil
	for _, c := range h.Cores {
		m.coreHists = append(m.coreHists, probeClamp(c, limit))
	}
	if len(h.Temps) > 0 {
		m.tempHist = clampMap(h.Temps)
	}
	if len(h.Freqs) > 0 {
		m.freqHist = clampMap(h.Freqs)
	}
	if len(h.Rx) > 0 {
		m.rxHist = clampMap(h.Rx)
	}
	if len(h.Tx) > 0 {
		m.txHist = clampMap(h.Tx)
	}
	if len(h.RTT) > 0 {
		m.rttHist = clampMap(h.RTT)
	}

	m.peaks.cpuPct = h.Peaks.CPUPct
	m.peaks.tempC = h.Peaks.TempC
	m.peaks.tempLabel = h.Peaks.TempLabel
	m.peaks.load1 = h.Peaks.Load1
	m.peaks.memPct = h.Peaks.MemPct
	if h.Peaks.BattMin >= 0 {
		m.peaks.battMin = h.Peaks.BattMin
	}
	m.restored = true
}

// Restore loads any persisted sparkline history into the model. Exported so
// the entry point can call it before the program starts; a failure is silent
// because losing a picture of the last few minutes is not worth a message.
//
// A successful restore does say so, briefly: a sparkline that starts out full
// of shape would otherwise read as live traffic that is not happening.
func (m *Model) Restore() {
	m.loadHistory()
	if m.restored {
		m.setNotice(subtleStyle.Render("sparkline history restored from the previous session"))
	}
}

// Save persists the sparkline history on exit.
func (m Model) Save() error { return m.saveHistory() }
