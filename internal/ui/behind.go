package ui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// fetchKind identifies a guarded sampling round. Every scheduled fetch goes
// through queue: the obviously slow ones (the /proc walk, the socket walk,
// vcgencmd + iw, ping), and the cheap ones too, because "cheap" holds only
// until a hung NFS mount wedges statfs or an I2C sensor stalls a hwmon read —
// and then an unguarded fetch stacks one more blocked goroutine every tick.
type fetchKind int

const (
	fetchProcs fetchKind = iota
	fetchConns
	fetchSys
	fetchLatency
	fetchCPU
	fetchMem
	fetchPSI
	fetchNet
	fetchDisk
	fetchPower
	fetchHealth
	fetchDetail
	fetchKmsg
	numFetchKinds
)

func (f fetchKind) String() string {
	switch f {
	case fetchCPU:
		return "cpu"
	case fetchMem:
		return "memory"
	case fetchPSI:
		return "pressure"
	case fetchNet:
		return "network"
	case fetchDisk:
		return "disk"
	case fetchPower:
		return "power"
	case fetchHealth:
		return "storage/modem"
	case fetchDetail:
		return "process detail"
	case fetchKmsg:
		return "kernel log"
	case fetchConns:
		return "connections"
	case fetchSys:
		return "gpu/soc"
	case fetchLatency:
		return "ping"
	default:
		return "processes"
	}
}

// behindTTL is how long the header keeps naming a lagging probe after it
// catches up, so a brief stall is still visible to someone glancing over.
const behindTTL = 10 * time.Second

// queue dispatches a fetch unless its previous round is still running, in
// which case it records that this probe is falling behind its interval rather
// than piling another goroutine onto the sampler. A nil command means the
// fetch is disabled (ping with ping_enabled = false), which is not lateness.
func (m *Model) queue(cmds []tea.Cmd, k fetchKind, build func() tea.Cmd) []tea.Cmd {
	if m.inFlight[k] {
		m.behindSkips[k]++
		m.behindAt = time.Now()
		return cmds
	}
	cmd := build()
	if cmd == nil {
		return cmds
	}
	m.inFlight[k] = true
	m.behindSkips[k] = 0 // dispatched on schedule: this probe has caught up
	return append(cmds, cmd)
}

// queueUser dispatches a refresh a keypress asked for. It shares the
// in-flight guard — two concurrent vcgencmd rounds are no more welcome here —
// but colliding with a scheduled round is not sampling lateness, and counting
// it as such lit the header's "slow" badge every time someone held down the
// brightness key.
func (m *Model) queueUser(k fetchKind, build func() tea.Cmd) tea.Cmd {
	if m.inFlight[k] {
		return nil
	}
	cmd := build()
	if cmd == nil {
		return nil
	}
	m.inFlight[k] = true
	return cmd
}

// done clears a probe's guard when its reply lands.
func (m *Model) done(k fetchKind) { m.inFlight[k] = false }

// behindBadge names the probes that recently could not keep up. On a CM4 the
// difference between "something is slow" and "vcgencmd is slow" is the whole
// diagnosis, so the badge lists the kinds rather than a bare warning.
func (m Model) behindBadge() string {
	if m.behindAt.IsZero() || time.Since(m.behindAt) > behindTTL {
		return ""
	}
	var slow []string
	for k := fetchKind(0); k < numFetchKinds; k++ {
		if m.behindSkips[k] > 0 {
			slow = append(slow, k.String())
		}
	}
	if len(slow) == 0 {
		return ""
	}
	return warnStyle.Render("⏳ slow: " + strings.Join(slow, ", "))
}
