package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/probe"
)

// oomTracker turns the kernel's cumulative oom_kill counter into events. A
// process the OOM killer takes just vanishes — no exit message, no prompt —
// so the only trace a user would otherwise see is the program being gone.
type oomTracker struct {
	primed  bool
	last    uint64      // counter value at the previous sample
	session uint64      // kills seen since ucmon started
	events  []oomEvent  // kills inside the alert window, oldest first
	victims []oomVictim // named by the kernel log, newest last
	logged  int         // kills synthesized from the log (no counter)
}

// oomVictim is a kill the kernel log named. The counter only says how many.
type oomVictim struct {
	at   time.Time
	pid  int
	comm string
}

// oomVictimMatch is how far apart the counter and the log line may be and
// still describe the same kill: the log line carries the kernel's own time,
// the counter the time it was sampled, at most a tick or two later.
const oomVictimMatch = 10 * time.Second

type oomEvent struct {
	at  time.Time
	n   uint64 // processes killed between two samples
	key string // the alert's identity: the counter value after the kill
}

// oomEventKeep bounds the event list however long the alert window is set.
const oomEventKeep = 32

// observe records a new counter reading. The first one is only a baseline:
// kills from before ucmon started are history, not something to alert on.
// window prunes events too old to keep their alert up.
func (o *oomTracker) observe(count uint64, now time.Time, window time.Duration) {
	if o.primed && count > o.last {
		n := count - o.last
		o.session += n
		o.events = append(o.events, oomEvent{at: now, n: n, key: fmt.Sprintf("oom:%d", count)})
	}
	// A counter that went backwards (a fixture, or a checkpoint/restore) is
	// a new baseline, not a negative kill count.
	o.primed, o.last = true, count
	o.prune(now, window)
}

func (o *oomTracker) prune(now time.Time, window time.Duration) {
	keep := o.events[:0]
	for _, e := range o.events {
		if now.Sub(e.at) < window {
			keep = append(keep, e)
		}
	}
	if len(keep) > oomEventKeep {
		keep = keep[len(keep)-oomEventKeep:]
	}
	o.events = keep
}

// addVictim records a victim the kernel log named. Without the counter
// (a kernel before 4.13, or a restricted /proc) the log line is the only
// sign of the kill, so it becomes the event itself.
func (o *oomTracker) addVictim(at time.Time, pid int, comm string, counted bool, window time.Duration) {
	o.victims = append(o.victims, oomVictim{at: at, pid: pid, comm: comm})
	if len(o.victims) > oomEventKeep {
		o.victims = o.victims[len(o.victims)-oomEventKeep:]
	}
	if !counted {
		o.logged++
		o.session++
		o.events = append(o.events, oomEvent{at: at, n: 1, key: fmt.Sprintf("oom:log%d", o.logged)})
		o.prune(at, window)
	}
}

// named lists the victims attributed to a kill: each victim belongs to the
// one kill nearest it in time, and a kill takes at most as many names as it
// has victims — two kills seconds apart must not each claim both.
func (o *oomTracker) named(e oomEvent) []string {
	var out []string
	for _, v := range o.victims {
		best, bestD := -1, oomVictimMatch
		for i, ev := range o.events {
			d := v.at.Sub(ev.at)
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = i, d
			}
		}
		if best >= 0 && o.events[best].key == e.key && uint64(len(out)) < e.n {
			out = append(out, fmt.Sprintf("%s (pid %d)", v.comm, v.pid))
		}
	}
	return out
}

// conds adds one critical condition per kill still inside the window. Each
// kill gets its own key, so a second kill while the first is still shown
// raises (bell, alert_cmd, log line) instead of silently rewording it.
func (o *oomTracker) conds(conds map[string]alertCond, now time.Time, window time.Duration) {
	for _, e := range o.events {
		if now.Sub(e.at) >= window {
			continue
		}
		var what string
		names := o.named(e)
		switch {
		case len(names) > 0 && uint64(len(names)) == e.n:
			what = strings.Join(names, ", ")
		case len(names) > 0:
			what = fmt.Sprintf("%s and %d more (dmesg names them)", strings.Join(names, ", "), e.n-uint64(len(names)))
		case e.n > 1:
			what = fmt.Sprintf("%d processes (dmesg names them)", e.n)
		default:
			what = "a process (dmesg names it)"
		}
		conds[e.key] = alertCond{sevCrit,
			fmt.Sprintf("out of memory: the kernel killed %s at %s", what, e.at.Format("15:04:05"))}
	}
}

// eventWindow is how long an event (an OOM kill, a kernel-log error) keeps
// its alert up after it was last seen (0: event alerts off).
func (m Model) eventWindow() time.Duration {
	return time.Duration(m.cfg.EventAlertMinutes * float64(time.Minute))
}

// alertNow is the clock event alerts expire against: the latest tick, so a
// stalled probe cannot freeze an alert on screen. Before the first tick
// (tests, --once) the latest memory sample stands in.
func (m Model) alertNow() time.Time {
	if !m.now.IsZero() {
		return m.now
	}
	if !m.memSnap.TakenAt.IsZero() {
		return m.memSnap.TakenAt
	}
	return time.Now()
}

// oomCountMsg is the OOM counter alone, read while sampling is paused: the
// memory readings stay frozen, but a kill must still be seen when it happens.
type oomCountMsg struct {
	n  uint64
	ok bool
	at time.Time
}

func fetchOOMCountCmd() tea.Cmd {
	return func() tea.Msg {
		n, ok := probe.ReadOOMKills()
		return oomCountMsg{n, ok, time.Now()}
	}
}
