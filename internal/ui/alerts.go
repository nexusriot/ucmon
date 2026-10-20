package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/nexusriot/ucmon/internal/probe"
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

// activeCond wraps a condition with its flap-damping state.
type activeCond struct {
	cond    alertCond
	missing int // consecutive updates the condition was absent
}

// alertEngine tracks active conditions and an edge-triggered log of changes.
// Conditions are keyed by a stable identity (e.g. "temp:cpu_thermal") so the
// same sensor crossing a threshold logs once, not every tick. A condition
// must stay absent for clearTicks consecutive updates before it clears, so a
// value oscillating around its threshold doesn't spam raise/clear pairs.
type alertEngine struct {
	active     map[string]activeCond
	log        []alertEntry
	cap        int
	clearTicks int
	muted      bool // banner suppressed; the log keeps recording
}

func newAlertEngine(capN, clearTicks int) alertEngine {
	if capN < 16 {
		capN = 16
	}
	if clearTicks < 1 {
		clearTicks = 1
	}
	return alertEngine{active: map[string]activeCond{}, cap: capN, clearTicks: clearTicks}
}

func (e *alertEngine) push(en alertEntry) {
	e.log = append(e.log, en)
	if len(e.log) > e.cap {
		e.log = e.log[len(e.log)-e.cap:]
	}
}

// update diffs current conditions against the active set, logging new alerts,
// severity escalations, and (damped) clears. It returns the events recorded
// this tick so the caller can run delivery side effects (bell, hook, file).
func (e *alertEngine) update(conds map[string]alertCond, now time.Time) []alertEntry {
	var events []alertEntry

	for k, c := range conds {
		prev, ok := e.active[k]
		switch {
		case !ok:
			events = append(events, alertEntry{when: now, sev: c.sev, msg: c.msg})
		case c.sev > prev.cond.sev:
			events = append(events, alertEntry{when: now, sev: c.sev, msg: c.msg})
		}
		// De-escalation or text change is tracked silently; presence always
		// resets the absent counter.
		e.active[k] = activeCond{cond: c}
	}

	for k, prev := range e.active {
		if _, ok := conds[k]; ok {
			continue
		}
		prev.missing++
		if prev.missing >= e.clearTicks {
			delete(e.active, k)
			events = append(events, alertEntry{
				when: now, sev: prev.cond.sev, msg: prev.cond.msg, cleared: true,
			})
		} else {
			e.active[k] = prev
		}
	}

	for _, ev := range events {
		e.push(ev)
	}
	return events
}

// summary returns the most severe active condition and the active count.
//
// The tie-break on the message matters: active conditions live in a map, so
// picking the first of several equally severe ones left the banner choosing a
// different message on every frame — four hot sensors made the footer cycle
// between them several times a second. It is also the order activeList uses,
// so the banner names the condition the Alerts tab lists first.
func (e *alertEngine) summary() (top alertCond, count int, any bool) {
	if len(e.active) == 0 {
		return alertCond{}, 0, false
	}
	first := true
	for _, c := range e.active {
		switch {
		case first:
			top, first = c.cond, false
		case c.cond.sev > top.sev:
			top = c.cond
		case c.cond.sev == top.sev && c.cond.msg < top.msg:
			top = c.cond
		}
	}
	return top, len(e.active), true
}

// activeList returns the active conditions sorted critical-first then by text,
// for stable rendering on the Alerts tab.
func (e *alertEngine) activeList() []alertCond {
	out := make([]alertCond, 0, len(e.active))
	for _, c := range e.active {
		out = append(out, c.cond)
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

// handleAlertEvents runs the configured delivery actions for freshly logged
// events: terminal bell on a new critical, the alert_cmd hook, and the
// on-disk alert log.
func (m *Model) handleAlertEvents(events []alertEntry) {
	if len(events) == 0 {
		return
	}
	if m.cfg.AlertBell && !m.alerts.muted {
		for _, ev := range events {
			if !ev.cleared && ev.sev == sevCrit {
				fmt.Fprint(os.Stderr, "\a")
				break
			}
		}
	}
	if m.cfg.AlertLogFile != "" {
		if err := appendAlertLog(m.cfg.AlertLogFile, events); err != nil {
			m.setNotice(errStyle.Render("alert log: " + err.Error()))
		}
	}
	if m.cfg.AlertCmd != "" {
		for _, ev := range events {
			go runAlertCmd(m.cfg.AlertCmd, ev)
		}
	}
}

// appendAlertLog appends one line per event; events are rare (edge-triggered)
// so open-append-close keeps this simple and crash-safe.
func appendAlertLog(path string, events []alertEntry) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, ev := range events {
		if _, err := f.WriteString(formatAlertLogLine(ev)); err != nil {
			return err
		}
	}
	return nil
}

func formatAlertLogLine(ev alertEntry) string {
	state := "RAISE"
	if ev.cleared {
		state = "CLEAR"
	}
	return fmt.Sprintf("%s %s %s %s\n",
		ev.when.Format("2006-01-02 15:04:05"), state, ev.sev.label(), ev.msg)
}

// alertCmdEnv builds the UCMON_ALERT_* environment for the alert_cmd hook.
func alertCmdEnv(ev alertEntry) []string {
	state := "raise"
	if ev.cleared {
		state = "clear"
	}
	return []string{
		"UCMON_ALERT_STATE=" + state,
		"UCMON_ALERT_SEV=" + ev.sev.label(),
		"UCMON_ALERT_MSG=" + ev.msg,
	}
}

func runAlertCmd(cmdline string, ev alertEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", cmdline)
	cmd.Env = append(os.Environ(), alertCmdEnv(ev)...)
	_ = cmd.Run()
}

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

	if n := m.powerSnap.Load.CPUCount; n > 0 {
		if limit := float64(n) * c.LoadWarnFactor; m.powerSnap.Load.Load1 >= limit {
			conds["load"] = alertCond{sevWarn,
				fmt.Sprintf("load %.2f ≥ %.1f (%.1f×%d cores)",
					m.powerSnap.Load.Load1, limit, c.LoadWarnFactor, n)}
		}
	}

	// WiFi signal strength (readings are negative dBm; 0 disables).
	if c.WifiLowDBm != 0 {
		for _, ii := range m.netSnap.Ifaces {
			if !ii.IsUp || !ii.WiFi.Present || ii.WiFi.SignalDBm >= 0 {
				continue
			}
			if int(ii.WiFi.SignalDBm) <= c.WifiLowDBm {
				conds["wifi:"+ii.Name] = alertCond{sevWarn,
					fmt.Sprintf("%s signal %.0f dBm ≤ %d dBm", ii.Name, ii.WiFi.SignalDBm, c.WifiLowDBm)}
			}
		}
	}

	// Packet loss to a ping target (needs a settled window of samples).
	if c.PingEnabled && c.PingLossWarnPct > 0 {
		for target, win := range m.lossWin {
			if len(win) < 10 {
				continue
			}
			if loss := lossPct(win); loss >= c.PingLossWarnPct {
				conds["ping:"+target] = alertCond{sevWarn,
					fmt.Sprintf("packet loss to %s %.0f%% ≥ %.0f%%", target, loss, c.PingLossWarnPct)}
			}
		}
	}

	// Storage wear. A card at 90% of its rated write endurance looks perfect
	// in every throughput and usage reading right up to the day it goes
	// read-only, so this is the one condition nothing else can hint at.
	for _, h := range m.storage {
		switch h.Severity() {
		case 2:
			conds["storage:"+h.Device] = alertCond{sevCrit, storageAlertMsg(h)}
		case 1:
			conds["storage:"+h.Device] = alertCond{sevWarn, storageAlertMsg(h)}
		}
	}

	// Modem link. Only meaningful once the user has asked for modem polling.
	if c.ModemEnabled && m.modem.Present && !m.modem.Registered() {
		msg := fmt.Sprintf("modem not registered (%s)", m.modem.State)
		if m.modem.FailedReason != "" {
			msg += ": " + m.modem.FailedReason
		}
		conds["modem"] = alertCond{sevWarn, msg}
	}

	// Rate-based conditions. A level threshold cannot tell a pack discharging
	// over eight hours from one dumping in twenty minutes; a slope can, and
	// it fires while there is still time to act on it.
	m.trendConds(conds)

	// PSI pressure ("some" share of the last 10s).
	if c.PSIWarnPct > 0 && m.psi.Present {
		for _, res := range []struct {
			key string
			r   probe.PSIRes
		}{{"cpu", m.psi.CPU}, {"mem", m.psi.Mem}, {"io", m.psi.IO}} {
			if res.r.HasSome && res.r.SomeAvg10 >= c.PSIWarnPct {
				conds["psi:"+res.key] = alertCond{sevWarn,
					fmt.Sprintf("%s pressure %.0f%% ≥ %.0f%% (10s some)", res.key, res.r.SomeAvg10, c.PSIWarnPct)}
			}
		}
	}

	return conds
}

// storageAlertMsg describes why a device is failing its health check, in the
// terms the device itself reports.
func storageAlertMsg(h probe.StorageHealth) string {
	switch {
	case h.ReadOnly:
		return fmt.Sprintf("%s (%s) has gone read-only — the controller retired it", h.Device, h.Kind)
	case h.PreEOL == 3:
		return fmt.Sprintf("%s (%s) reserve blocks exhausted — replace it", h.Device, h.Kind)
	case h.PreEOL == 2:
		return fmt.Sprintf("%s (%s) reserve blocks 80%% used", h.Device, h.Kind)
	}
	if pct, ok := h.WearPct(); ok {
		return fmt.Sprintf("%s (%s) at %.0f%% of rated write endurance", h.Device, h.Kind, pct)
	}
	return fmt.Sprintf("%s (%s) reports degraded health", h.Device, h.Kind)
}

// trendConds adds the predictive conditions: where a reading is heading and
// how soon it gets there.
func (m Model) trendConds(conds map[string]alertCond) {
	c := m.cfg

	// A battery draining fast enough to matter, while there is still charge
	// to act on. The flat batt_low_pct alert only speaks once it is nearly
	// too late.
	if c.BattDrainWarnPctH > 0 {
		b := m.powerSnap.Battery
		if b.Present && b.Capacity >= 0 && b.Status != "Charging" && b.Status != "Full" {
			if perHour, ok := m.trends.rate("batt"); ok && -perHour >= c.BattDrainWarnPctH {
				msg := fmt.Sprintf("battery draining %.0f%%/h", -perHour)
				if eta, ok := m.trends.etaTo("batt", float64(b.Capacity), 0); ok {
					msg += fmt.Sprintf(" — empty in %s", humanETA(eta))
				}
				conds["batt:rate"] = alertCond{sevWarn, msg}
			}
		}
	}

	// A filesystem filling fast enough to run out within the horizon. On a
	// 16 GB card the gap between "90% full" and "full" can be minutes.
	if c.DiskFullWarnHours > 0 {
		for _, d := range m.disks {
			if d.FSType == "squashfs" || d.UsedPct >= 100 {
				continue
			}
			key := "disk:" + d.MountPoint
			eta, ok := m.trends.etaTo(key, d.UsedPct, 100)
			if !ok || eta.Hours() > c.DiskFullWarnHours {
				continue
			}
			sev := sevWarn
			if eta < time.Hour {
				sev = sevCrit
			}
			conds["diskfill:"+d.MountPoint] = alertCond{sev,
				fmt.Sprintf("%s filling — full in %s (now %.0f%%)", d.MountPoint, humanETA(eta), d.UsedPct)}
		}
	}

	// A sensor climbing toward its own critical threshold. Catches a failed
	// fan or a blocked vent before the throttle flags do.
	if c.TempRiseWarnCPerMin > 0 {
		for _, t := range m.cpuSnap.Temperatures {
			perHour, ok := m.trends.rate("temp:" + t.Label)
			if !ok {
				continue
			}
			perMin := perHour / 60
			if perMin < c.TempRiseWarnCPerMin || t.Temp >= c.TempCrit {
				continue // already-critical sensors are the level alert's job
			}
			msg := fmt.Sprintf("%s rising %.1f°C/min (now %.0f°C)", t.Label, perMin, t.Temp)
			if eta, ok := m.trends.etaTo("temp:"+t.Label, t.Temp, c.TempCrit); ok {
				msg += fmt.Sprintf(" — %.0f°C in %s", c.TempCrit, humanETA(eta))
			}
			conds["temprise:"+t.Label] = alertCond{sevWarn, msg}
		}
	}
}

// lossPct is the failure share of a ping success/failure window, in percent.
func lossPct(win []bool) float64 {
	if len(win) == 0 {
		return 0
	}
	fails := 0
	for _, ok := range win {
		if !ok {
			fails++
		}
	}
	return float64(fails) / float64(len(win)) * 100
}
