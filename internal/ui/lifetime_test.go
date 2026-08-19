package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func ifaceSnap(names ...string) probe.NetSnapshot {
	snap := probe.NetSnapshot{TakenAt: nonZeroTime()}
	for _, n := range names {
		snap.Ifaces = append(snap.Ifaces, probe.IfaceInfo{Name: n, IsUp: true, RxBps: 10, TxBps: 5})
	}
	return snap
}

func applyNet(t *testing.T, m Model, snap probe.NetSnapshot) Model {
	t.Helper()
	mm, _ := m.Update(netMsg(snap))
	return mm.(Model)
}

// Container veths, a reconnecting VPN or ppp churn interface names; their
// throughput history must not accumulate for the lifetime of the process.
func TestIfaceHistoryEvictedOnChurn(t *testing.T) {
	m := testModel()
	for gen := 0; gen < 500; gen++ {
		m = applyNet(t, m, ifaceSnap("wlan0", fmt.Sprintf("veth%06x", gen)))
	}
	if len(m.rxHist) > 2 || len(m.txHist) > 2 {
		t.Errorf("after 500 churns: rxHist=%d txHist=%d, want <= 2",
			len(m.rxHist), len(m.txHist))
	}
	if _, ok := m.rxHist["wlan0"]; !ok {
		t.Error("the surviving interface lost its history")
	}
}

// An interface that is merely down keeps its history; only one that leaves the
// snapshot entirely is evicted.
func TestDownIfaceKeepsHistory(t *testing.T) {
	m := testModel()
	m = applyNet(t, m, ifaceSnap("wlan0", "eth0"))
	if len(m.rxHist) != 2 {
		t.Fatalf("rxHist = %d, want 2", len(m.rxHist))
	}

	down := ifaceSnap("wlan0")
	down.Ifaces = append(down.Ifaces, probe.IfaceInfo{Name: "eth0", IsUp: false})
	m = applyNet(t, m, down)
	if _, ok := m.rxHist["eth0"]; !ok {
		t.Error("a down interface lost its history")
	}

	m = applyNet(t, m, ifaceSnap("wlan0"))
	if _, ok := m.rxHist["eth0"]; ok {
		t.Error("a removed interface kept its history")
	}
}

// An empty snapshot is suspect (net.Interfaces always reports at least lo), so
// it must not wipe everything.
func TestEmptySnapshotKeepsHistory(t *testing.T) {
	m := testModel()
	m = applyNet(t, m, ifaceSnap("wlan0"))
	m = applyNet(t, m, probe.NetSnapshot{TakenAt: nonZeroTime()})
	if len(m.rxHist) != 1 {
		t.Errorf("rxHist = %d after an empty snapshot, want 1", len(m.rxHist))
	}
}

// Selection follows the interface by name: one going away must not silently
// re-point the cursor at whichever interface shifted into that slot.
func TestIfaceSelectionFollowsName(t *testing.T) {
	m := testModel()
	m.activeTab = tabNet
	m = applyNet(t, m, ifaceSnap("eth0", "wlan0", "wg0"))

	m, _ = press(t, m, key('j')) // none -> eth0
	m, _ = press(t, m, key('j')) // eth0 -> wlan0
	if m.netIfaceName != "wlan0" {
		t.Fatalf("selection = %q, want wlan0", m.netIfaceName)
	}

	// eth0 disappears; wlan0 shifts from index 1 to index 0.
	m = applyNet(t, m, ifaceSnap("wlan0", "wg0"))
	if m.netIfaceName != "wlan0" {
		t.Errorf("selection moved to %q when eth0 went away", m.netIfaceName)
	}
	if got := m.netIfaceIdx(); got != 0 {
		t.Errorf("netIfaceIdx = %d, want 0", got)
	}

	// The selected interface itself disappearing deselects rather than
	// pointing at a stranger.
	m = applyNet(t, m, ifaceSnap("wg0"))
	if got := m.netIfaceIdx(); got != -1 {
		t.Errorf("netIfaceIdx = %d after the selection vanished, want -1", got)
	}
}

// j and k cycle forwards and backwards through the up interfaces and back to
// "nothing selected".
func TestIfaceSelectionCycles(t *testing.T) {
	m := testModel()
	m.activeTab = tabNet
	m = applyNet(t, m, ifaceSnap("eth0", "wlan0"))

	for _, want := range []string{"eth0", "wlan0", "", "eth0"} {
		m, _ = press(t, m, key('j'))
		if m.netIfaceName != want {
			t.Fatalf("j: selection = %q, want %q", m.netIfaceName, want)
		}
	}
	for _, want := range []string{"", "wlan0", "eth0", ""} {
		m, _ = press(t, m, key('k'))
		if m.netIfaceName != want {
			t.Fatalf("k: selection = %q, want %q", m.netIfaceName, want)
		}
	}
}

// A tick must not queue a second /proc walk, vcgencmd round or ping round
// while the previous one is still running.
func TestSlowFetchesAreNotQueuedTwice(t *testing.T) {
	m := testModel()
	for k := fetchKind(0); k < numFetchKinds; k++ {
		m.inFlight[k] = true
	}
	for _, c := range []struct {
		k     fetchKind
		build func() tea.Cmd
	}{
		{fetchProcs, m.fetchProcsCmd},
		{fetchSys, m.fetchSysCmd},
		{fetchLatency, m.fetchLatencyCmd},
	} {
		if got := m.queue(nil, c.k, c.build); len(got) != 0 {
			t.Errorf("%s: a second fetch was queued while one was in flight", c.k)
		}
		if m.behindSkips[c.k] != 1 {
			t.Errorf("%s: skip not recorded (%d)", c.k, m.behindSkips[c.k])
		}
	}
	if m.behindBadge() == "" {
		t.Error("no badge after every probe fell behind")
	}

	for k := fetchKind(0); k < numFetchKinds; k++ {
		m.inFlight[k] = false
	}
	if got := m.queue(nil, fetchProcs, m.fetchProcsCmd); len(got) != 1 {
		t.Error("an idle fetch was declined")
	}
	if m.behindSkips[fetchProcs] != 0 {
		t.Error("catching up did not clear the skip count")
	}
}

// Guards are raised on dispatch and lowered by the reply — including the
// failure reply, or the fetch would never run again.
func TestInFlightGuardLifecycle(t *testing.T) {
	m := testModel()
	m.procEvery, m.slowEvery = 1, 1

	m, _ = press(t, m, tickMsg(time.Now()))
	if !m.inFlight[fetchProcs] || !m.inFlight[fetchSys] {
		t.Fatalf("guards not raised: %v", m.inFlight)
	}

	// A second tick while both are in flight must not clear them.
	m, _ = press(t, m, tickMsg(time.Now()))
	if !m.inFlight[fetchProcs] || !m.inFlight[fetchSys] {
		t.Error("guards cleared by a tick instead of by the reply")
	}

	m, _ = press(t, m, procMsg{err: fmt.Errorf("permission denied")})
	if m.inFlight[fetchProcs] {
		t.Error("a failed process fetch left its guard raised")
	}
	if m.err == nil {
		t.Error("the fetch error was not surfaced")
	}

	m, _ = press(t, m, sysMsg{})
	if m.inFlight[fetchSys] {
		t.Error("the sys guard was not lowered by its reply")
	}
}

// Transient status lines fade instead of sitting there until an unrelated key
// happens to overwrite them.
func TestNoticesExpire(t *testing.T) {
	var n notice
	if got := n.get(); got != "" {
		t.Errorf("empty notice returned %q", got)
	}

	n.set("sent SIGTERM to sshd (1234)")
	if got := n.get(); got == "" {
		t.Error("a fresh notice is not visible")
	}

	n.until = time.Now().Add(-time.Second)
	if got := n.get(); got != "" {
		t.Errorf("an expired notice still returned %q", got)
	}

	n.set("again")
	n.clear()
	if got := n.get(); got != "" {
		t.Errorf("a cleared notice still returned %q", got)
	}
}

// The kill confirmation's status line is one of those transient lines.
func TestKillNoticeExpires(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{{PID: 1, Name: "systemd", Status: "sleep"}}
	m.refreshProcs()

	// The column-header hint mentions SIGTERM permanently, so assert on a
	// marker that can only come from the notice itself.
	const marker = "sent-signal-marker"
	m.setProcsNotice(marker)
	if !strings.Contains(m.viewProcs(), marker) {
		t.Fatal("the kill notice was not rendered")
	}

	m.procsNotice.until = time.Now().Add(-time.Second)
	if strings.Contains(m.viewProcs(), marker) {
		t.Error("the kill notice outlived its TTL")
	}
}

// The badge names which probe is late, not merely that something is: on a CM4
// "gpu/soc" points straight at the vcgencmd shell-outs.
func TestBehindBadgeNamesTheLateProbe(t *testing.T) {
	m := testModel()
	if m.behindBadge() != "" {
		t.Fatal("a fresh model reports being behind")
	}

	m.inFlight[fetchSys] = true
	m.queue(nil, fetchSys, m.fetchSysCmd)

	badge := m.behindBadge()
	if !strings.Contains(badge, "gpu/soc") {
		t.Errorf("badge = %q, want it to name gpu/soc", badge)
	}
	if strings.Contains(badge, "processes") || strings.Contains(badge, "ping") {
		t.Errorf("badge names probes that kept up: %q", badge)
	}

	m.w, m.h = 120, 40
	if !strings.Contains(m.renderHeader(), "gpu/soc") {
		t.Error("the badge is not in the header")
	}
}

// It fades once the probe catches up, so a brief stall does not stick.
func TestBehindBadgeFades(t *testing.T) {
	m := testModel()
	m.inFlight[fetchProcs] = true
	m.queue(nil, fetchProcs, m.fetchProcsCmd)
	if m.behindBadge() == "" {
		t.Fatal("no badge after a skip")
	}

	m.behindAt = time.Now().Add(-behindTTL - time.Second)
	if got := m.behindBadge(); got != "" {
		t.Errorf("badge outlived its TTL: %q", got)
	}

	// A dispatch that goes through clears the count, so the badge stays gone
	// even if another probe is late later.
	m.behindAt = time.Now()
	m.inFlight[fetchProcs] = false
	m.queue(nil, fetchProcs, m.fetchProcsCmd)
	if got := m.behindBadge(); got != "" {
		t.Errorf("badge survived the probe catching up: %q", got)
	}
}

// A disabled probe is not a late one: ping_enabled = false must not light the
// badge every tick.
func TestDisabledProbeIsNotBehind(t *testing.T) {
	cfg := config.Default()
	cfg.PingEnabled = false
	m := NewModel(cfg)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(Model)

	for i := 0; i < 5; i++ {
		m.queue(nil, fetchLatency, m.fetchLatencyCmd)
	}
	if got := m.behindBadge(); got != "" {
		t.Errorf("a disabled probe reported lateness: %q", got)
	}
	if m.behindSkips[fetchLatency] != 0 {
		t.Errorf("skips recorded for a disabled probe: %d", m.behindSkips[fetchLatency])
	}
}
