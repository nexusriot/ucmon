package ui

import (
	"fmt"
	"time"
)

// trendWindow is how far back a rate is measured over. Long enough that a
// one-tick wobble cannot set a slope, short enough to notice a real one
// within a couple of minutes.
const trendWindow = 2 * time.Minute

// trendMinSpan is the shortest baseline a rate may be computed from. Below
// it, dividing a small change by a small interval produces a confident-looking
// number built from noise.
const trendMinSpan = 45 * time.Second

// trendPoint is one observation of a monotonic-ish reading.
type trendPoint struct {
	at time.Time
	v  float64
}

// trendTracker holds a short history per metric and answers "how fast is this
// moving, and when does it hit the wall".
//
// Every alert in ucmon until now was a level: it fires once the battery is
// already at 5% or the disk is already at 90%. A level cannot tell a pack
// discharging over eight hours from one dumping in twenty minutes, and by the
// time a filesystem reads 90% on a 16 GB card there may be minutes left. A
// slope can, and it is free — the readings are already arriving.
type trendTracker struct {
	series map[string][]trendPoint
}

func newTrendTracker() trendTracker {
	return trendTracker{series: map[string][]trendPoint{}}
}

// observe records a reading, dropping anything older than the window.
func (t *trendTracker) observe(key string, v float64, now time.Time) {
	pts := append(t.series[key], trendPoint{at: now, v: v})
	cut := now.Add(-trendWindow)
	first := 0
	for first < len(pts) && pts[first].at.Before(cut) {
		first++
	}
	// Keep one sample behind the cut so the baseline spans the whole window.
	if first > 0 {
		first--
	}
	t.series[key] = pts[first:]
}

// rate returns the change per hour across the retained window, and whether
// the baseline is long enough to mean anything.
func (t trendTracker) rate(key string) (perHour float64, ok bool) {
	pts := t.series[key]
	if len(pts) < 2 {
		return 0, false
	}
	first, last := pts[0], pts[len(pts)-1]
	span := last.at.Sub(first.at)
	if span < trendMinSpan {
		return 0, false
	}
	return (last.v - first.v) / span.Hours(), true
}

// etaTo returns how long until a reading moving at its current rate reaches
// target, and whether it is heading there at all.
func (t trendTracker) etaTo(key string, current, target float64) (time.Duration, bool) {
	perHour, ok := t.rate(key)
	if !ok || perHour == 0 {
		return 0, false
	}
	remaining := target - current
	if remaining == 0 {
		return 0, false
	}
	// Moving away from the target, or the wrong way entirely.
	if (remaining > 0) != (perHour > 0) {
		return 0, false
	}
	hours := remaining / perHour
	if hours <= 0 {
		return 0, false
	}
	return time.Duration(hours * float64(time.Hour)), true
}

// reset clears every series, used when session peaks are reset.
func (t *trendTracker) reset() {
	t.series = map[string][]trendPoint{}
}

// recordTrend feeds the current readings into the trend tracker. It runs on
// every tick, including while paused — a frozen reading simply repeats, which
// correctly flattens the slope rather than leaving a stale one standing.
func (m *Model) recordTrend(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	if b := m.powerSnap.Battery; b.Present && b.Capacity >= 0 {
		m.trends.observe("batt", float64(b.Capacity), now)
	}
	for _, d := range m.disks {
		if d.FSType == "squashfs" {
			continue
		}
		m.trends.observe("disk:"+d.MountPoint, d.UsedPct, now)
	}
	for _, t := range m.cpuSnap.Temperatures {
		m.trends.observe("temp:"+t.Label, t.Temp, now)
	}
	if m.memSnap.Total > 0 {
		m.trends.observe("mem", m.memSnap.UsedPct, now)
	}
}

// humanETA renders a duration the way someone reading a warning wants it.
func humanETA(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1 min"
	case d < time.Hour:
		return fmt.Sprintf("%.0f min", d.Minutes())
	case d < 48*time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	default:
		return fmt.Sprintf("%.0f days", d.Hours()/24)
	}
}
