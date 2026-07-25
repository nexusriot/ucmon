package ui

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// alertSeverity orders alerts; higher wins for the header banner.
type alertSeverity int

const (
	sevWarn alertSeverity = iota
	sevCrit
)

func (s alertSeverity) label() string {
	if s == sevCrit {
		return "CRIT"
	}
	return "WARN"
}

func (s alertSeverity) style() lipgloss.Style {
	if s == sevCrit {
		return errStyle
	}
	return warnStyle
}

// alertCond is a single currently-true condition.
type alertCond struct {
	sev alertSeverity
	msg string
}

// alertEntry is one line in the historical alert log.
type alertEntry struct {
	when    time.Time
	sev     alertSeverity
	msg     string
	cleared bool // true = this records a condition resolving
}

// alertEngine tracks active conditions and an edge-triggered log of changes.
// Conditions are keyed by a stable identity (e.g. "temp:cpu_thermal") so the
// same sensor crossing a threshold logs once, not every tick.
type alertEngine struct {
	active map[string]alertCond
	log    []alertEntry
	cap    int
	muted  bool // banner suppressed; the log keeps recording
}

func newAlertEngine(capN int) alertEngine {
	if capN < 16 {
		capN = 16
	}
	return alertEngine{active: map[string]alertCond{}, cap: capN}
}

func (e *alertEngine) push(en alertEntry) {
	e.log = append(e.log, en)
	if len(e.log) > e.cap {
		e.log = e.log[len(e.log)-e.cap:]
	}
}

// update diffs current conditions against the active set, logging new alerts,
// severity escalations, and clears.
func (e *alertEngine) update(conds map[string]alertCond, now time.Time) {
	for k, c := range conds {
		prev, ok := e.active[k]
		switch {
		case !ok:
			e.active[k] = c
			e.push(alertEntry{when: now, sev: c.sev, msg: c.msg})
		case c.sev > prev.sev:
			e.active[k] = c
			e.push(alertEntry{when: now, sev: c.sev, msg: c.msg})
		default:
			// De-escalation or text change: track silently so a later
			// re-escalation is detected, but don't spam the log.
			e.active[k] = c
		}
	}
	for k, prev := range e.active {
		if _, ok := conds[k]; !ok {
			delete(e.active, k)
			e.push(alertEntry{when: now, sev: prev.sev, msg: prev.msg, cleared: true})
		}
	}
}

// summary returns the most severe active condition and the active count.
func (e *alertEngine) summary() (top alertCond, count int, any bool) {
	if len(e.active) == 0 {
		return alertCond{}, 0, false
	}
	first := true
	for _, c := range e.active {
		if first || c.sev > top.sev {
			top = c
			first = false
		}
	}
	return top, len(e.active), true
}

// activeList returns the active conditions sorted critical-first then by text,
// for stable rendering on the Alerts tab.
func (e *alertEngine) activeList() []alertCond {
	out := make([]alertCond, 0, len(e.active))
	for _, c := range e.active {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].sev != out[j].sev {
			return out[i].sev > out[j].sev
		}
		return out[i].msg < out[j].msg
	})
	return out
}

func (e *alertEngine) clearLog() {
	e.log = e.log[:0]
}

// ── Model integration ──

// currentAlertConds evaluates the latest probe snapshots against the configured
// thresholds and returns the set of conditions that are true right now.
func (m Model) currentAlertConds() map[string]alertCond {
	conds := map[string]alertCond{}
	c := m.cfg

	// CPU / SoC temperatures (reuse the hot/crit color thresholds).
	for _, t := range m.cpuSnap.Temperatures {
		switch {
		case t.Temp >= c.TempCrit:
			conds["temp:"+t.Label] = alertCond{sevCrit,
				fmt.Sprintf("%s %.0f°C ≥ %.0f°C critical", t.Label, t.Temp, c.TempCrit)}
		case t.Temp >= c.TempHot:
			conds["temp:"+t.Label] = alertCond{sevWarn,
				fmt.Sprintf("%s %.0f°C ≥ %.0f°C hot", t.Label, t.Temp, c.TempHot)}
		}
	}

	// Memory & swap.
	if m.memSnap.Total > 0 && m.memSnap.UsedPct >= c.MemWarnPct {
		conds["mem"] = alertCond{sevWarn,
			fmt.Sprintf("RAM %.0f%% used ≥ %.0f%%", m.memSnap.UsedPct, c.MemWarnPct)}
	}
	if m.memSnap.SwapTotal > 0 && m.memSnap.SwapUsedPct >= c.SwapWarnPct {
		conds["swap"] = alertCond{sevWarn,
			fmt.Sprintf("swap %.0f%% used ≥ %.0f%%", m.memSnap.SwapUsedPct, c.SwapWarnPct)}
	}

	// Filesystems. Skip squashfs (loop-mounted snaps sit at 100% by design).
	for _, d := range m.disks {
		if d.FSType == "squashfs" {
			continue
		}
		if d.UsedPct >= c.DiskWarnPct {
			conds["disk:"+d.MountPoint] = alertCond{sevWarn,
				fmt.Sprintf("disk %s %.0f%% full ≥ %.0f%%", d.MountPoint, d.UsedPct, c.DiskWarnPct)}
		}
	}

	// Battery (only while discharging — a low pack on the charger is fine).
	b := m.powerSnap.Battery
	if b.Present && b.Capacity >= 0 && b.Status != "Charging" && b.Status != "Full" {
		switch {
		case b.Capacity <= c.BattCritPct:
			conds["batt"] = alertCond{sevCrit,
				fmt.Sprintf("battery %d%% ≤ %d%% critical", b.Capacity, c.BattCritPct)}
		case b.Capacity <= c.BattLowPct:
			conds["batt"] = alertCond{sevWarn,
				fmt.Sprintf("battery %d%% ≤ %d%% low", b.Capacity, c.BattLowPct)}
		}
	}

	// Pi / uConsole throttling (current flags only — the "occurred" bits are
	// historical and would latch the banner on forever).
	th := m.powerSnap.Throttle
	if th.Available {
		if th.UnderVoltageNow {
			conds["throttle:uv"] = alertCond{sevCrit, "under-voltage detected"}
		}
		if th.ThrottledNow {
			conds["throttle:thr"] = alertCond{sevCrit, "CPU throttled now"}
		}
		if th.FreqCappedNow {
			conds["throttle:cap"] = alertCond{sevWarn, "ARM frequency capped"}
		}
		if th.SoftTempLimitNow {
			conds["throttle:soft"] = alertCond{sevWarn, "soft temperature limit active"}
		}
	}

	// Load average vs core count.
	if n := m.powerSnap.Load.CPUCount; n > 0 {
		if limit := float64(n) * c.LoadWarnFactor; m.powerSnap.Load.Load1 >= limit {
			conds["load"] = alertCond{sevWarn,
				fmt.Sprintf("load %.2f ≥ %.1f (%.1f×%d cores)",
					m.powerSnap.Load.Load1, limit, c.LoadWarnFactor, n)}
		}
	}

	return conds
}
