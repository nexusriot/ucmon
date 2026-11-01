package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

// memAt feeds one memory sample with the given oom_kill counter through
// Update, the same path a real fetch takes.
func memAt(t *testing.T, m Model, kills uint64, at time.Time) Model {
	t.Helper()
	next, _ := m.Update(memMsg(probe.MemSnapshot{
		Total: 1 << 30, Used: 1 << 29, UsedPct: 50,
		OOMKills: kills, OOMKillsOK: true, TakenAt: at,
	}))
	return next.(Model)
}

func oomConds(m Model) map[string]alertCond {
	out := map[string]alertCond{}
	for k, c := range m.currentAlertConds() {
		if strings.HasPrefix(k, "oom:") {
			out[k] = c
		}
	}
	return out
}

func TestOOMKillAlert(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	m := testModel()
	m.cfg.EventAlertMinutes = 10

	// Kills from before ucmon started are a baseline, not an alert.
	m = memAt(t, m, 5, t0)
	if c := oomConds(m); len(c) != 0 {
		t.Fatalf("baseline raised %v", c)
	}

	m = memAt(t, m, 6, t0.Add(time.Second))
	c := oomConds(m)
	if len(c) != 1 || c["oom:6"].sev != sevCrit || !strings.Contains(c["oom:6"].msg, "killed a process") {
		t.Fatalf("one kill: %+v", c)
	}

	// A second kill while the first is still up is its own alert, so it
	// raises (bell, hook) instead of rewording the first.
	m = memAt(t, m, 8, t0.Add(3*time.Minute))
	c = oomConds(m)
	if len(c) != 2 || !strings.Contains(c["oom:8"].msg, "killed 2 processes") {
		t.Fatalf("second kill: %+v", c)
	}
	eng := newAlertEngine(16, 1)
	raised := 0
	for _, e := range eng.update(m.currentAlertConds(), t0) {
		if strings.Contains(e.msg, "out of memory") && !e.cleared {
			raised++
		}
	}
	if raised != 2 {
		t.Errorf("%d OOM raises, want one per kill", raised)
	}

	if m.oom.session != 3 {
		t.Errorf("session kills = %d, want 3", m.oom.session)
	}
	m.activeTab = tabMem
	if v := m.viewMem(); !strings.Contains(v, "8 since boot") || !strings.Contains(v, "3 this session") {
		t.Error("Memory tab does not show the OOM counts")
	}
	if r := m.buildReport(t0); !strings.Contains(r, "OOM kills      8 since boot, 3 this session") {
		t.Error("report lacks the OOM line")
	}
	if p := m.buildPrometheus(); !strings.Contains(p, "ucmon_oom_kills_total 8") {
		t.Error("/metrics lacks ucmon_oom_kills_total")
	}
	if js := m.snapshotJSON(t0); js.Memory.OOMKills == nil || *js.Memory.OOMKills != 8 {
		t.Errorf("JSON oom_kills = %v", js.Memory.OOMKills)
	}

	// Each kill times out on its own.
	m = memAt(t, m, 8, t0.Add(11*time.Minute))
	if c := oomConds(m); len(c) != 1 || c["oom:8"].sev != sevCrit {
		t.Fatalf("after the first window: %+v", c)
	}
	m = memAt(t, m, 8, t0.Add(14*time.Minute))
	if c := oomConds(m); len(c) != 0 {
		t.Fatalf("after both windows: %+v", c)
	}

	// A counter that goes backwards is a new baseline.
	m = memAt(t, m, 2, t0.Add(15*time.Minute))
	if c := oomConds(m); len(c) != 0 {
		t.Fatalf("counter reset raised %v", c)
	}
}

func TestOOMKillAlertDisabled(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	m := testModel()
	m.cfg.EventAlertMinutes = 0
	m = memAt(t, m, 1, t0)
	m = memAt(t, m, 4, t0.Add(time.Second))
	if c := oomConds(m); len(c) != 0 {
		t.Fatalf("event_alert_minutes = 0 still raised %v", c)
	}
	if m.oom.session != 3 {
		t.Errorf("the counter is still tracked with the alert off: session = %d", m.oom.session)
	}
}

// Where the kernel has no counter, nothing about OOM is shown or exported.
func TestOOMKillsAbsent(t *testing.T) {
	m := testModel()
	m.memSnap = probe.MemSnapshot{Total: 1 << 30, TakenAt: time.Now()}
	m.activeTab = tabMem
	if strings.Contains(m.viewMem(), "OOM") || strings.Contains(m.buildPrometheus(), "oom_kills") {
		t.Error("OOM shown without a counter")
	}
	if js := m.snapshotJSON(time.Now()); js.Memory.OOMKills != nil {
		t.Error("JSON carries oom_kills without a counter")
	}
}
