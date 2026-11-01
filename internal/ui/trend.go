package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

// trendWindow is how far back a rate is measured over. Long enough that a
// one-tick wobble cannot set a slope, short enough to notice a real one
// within a couple of minutes.
const trendWindow = 2 * time.Minute

// trendMinSpan is the shortest baseline a rate may be computed from. Below
// it, dividing a small change by a small interval produces a confident-looking
// number built from noise.
const trendMinSpan = 45 * time.Second

// Battery capacity is an integer percent, so over trendWindow a single 1%
// step already reads as 30%/h — above the default batt_drain_warn_pct_h —
// and a healthy 10%/h discharge raised and cleared a warning on every step.
// Its series gets a window long enough that one step stays well below that.
const (
	battTrendWindow  = 10 * time.Minute
	battTrendMinSpan = 5 * time.Minute
)

// trendMaxHours caps an ETA: past it the reading is effectively flat, and
// time.Duration overflows (to a negative value on amd64) near 2.5e6 hours.
const trendMaxHours = 1e5

// A CPU warms by ten degrees in the first half-minute of any load and then
// levels off; over a 45 s baseline that burst reads as a climb toward
// critical. Temperatures get a baseline long enough to see the plateau.
const (
	tempTrendWindow  = 3 * time.Minute
	tempTrendMinSpan = 90 * time.Second
)

func trendSpans(key string) (window, minSpan time.Duration) {
	switch {
	case key == "batt":
		return battTrendWindow, battTrendMinSpan
	case strings.HasPrefix(key, "temp:"):
		return tempTrendWindow, tempTrendMinSpan
	}
	return trendWindow, trendMinSpan
}

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
//
// Times are compared on the wall clock: Go's monotonic reading stops while
// the system is suspended, so across a night in sleep the morning's first
// reading would sit minutes, not hours, after last night's — and a battery
// that drained 15% overnight would read as dumping it in five minutes. A gap
// longer than the window (a suspend, or a mount or sensor that went away and
// came back, perhaps as something else) starts the series over.
func (t *trendTracker) observe(key string, v float64, now time.Time) {
	now = now.Round(0)
	window, _ := trendSpans(key)
	pts := t.series[key]
	if n := len(pts); n > 0 && now.Sub(pts[n-1].at) > window {
		pts = nil
	}
	pts = append(pts, trendPoint{at: now, v: v})
	cut := now.Add(-window)
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

// evict forgets series not observed for longer than their window: the
// filesystems and sensors that are gone (a container's overlay mounts churn
// through dozens).
func (t *trendTracker) evict(now time.Time) {
	now = now.Round(0)
	for key, pts := range t.series {
		window, _ := trendSpans(key)
		if n := len(pts); n == 0 || now.Sub(pts[n-1].at) > window {
			delete(t.series, key)
		}
	}
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
	if _, minSpan := trendSpans(key); span < minSpan {
		return 0, false
	}
	return (last.v - first.v) / span.Hours(), true
}

// sustainedRate is rate, but only as fast as the reading is still moving:
// the smaller of the slope across the window and the slope across its most
// recent half. A warm-up burst that has levelled off has a steep window and a
// flat recent half, and is not a climb; a failed fan is steep in both.
func (t trendTracker) sustainedRate(key string) (perHour float64, ok bool) {
	whole, ok := t.rate(key)
	if !ok {
		return 0, false
	}
	pts := t.series[key]
	last := pts[len(pts)-1]
	mid := last.at.Add(-last.at.Sub(pts[0].at) / 2)
	// The baseline for the recent half is the latest reading at or before
	// its start.
	base := pts[0]
	for _, p := range pts {
		if p.at.After(mid) {
			break
		}
		base = p
	}
	if span := last.at.Sub(base.at); span > 0 {
		if recent := (last.v - base.v) / span.Hours(); math.Abs(recent) < math.Abs(whole) {
			return recent, true
		}
	}
	return whole, true
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
	if hours <= 0 || hours > trendMaxHours {
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
	m.trends.evict(now)
	if b := m.powerSnap.Battery; b.Present && b.Capacity >= 0 {
		m.trends.observe("batt", float64(b.Capacity), now)
	}
	for _, d := range m.disks {
		if probe.ReadOnlyByDesign(d.FSType) {
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
