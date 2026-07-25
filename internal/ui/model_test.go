package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func testModel() Model {
	m := NewModel(config.Default())
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm.(Model)
}

func key(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func press(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	mm, cmd := m.Update(msg)
	return mm.(Model), cmd
}

func TestPauseToggle(t *testing.T) {
	m := testModel()

	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if !m.paused {
		t.Fatal("space did not pause")
	}

	// A tick while paused must not advance the fetch schedule.
	before := m.tickCount
	m, cmd := press(t, m, tickMsg(time.Now()))
	if m.tickCount != before {
		t.Error("tickCount advanced while paused")
	}
	if cmd == nil {
		t.Error("paused tick must still reschedule the heartbeat")
	}

	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeySpace})
	if m.paused {
		t.Error("space did not resume")
	}
}

func TestHelpOverlayToggle(t *testing.T) {
	m := testModel()

	m, _ = press(t, m, key('?'))
	if !m.showHelp {
		t.Fatal("? did not open help")
	}
	if !strings.Contains(m.View(), "key bindings") {
		t.Error("help view not rendered")
	}

	// Other keys are swallowed while help is open.
	m, _ = press(t, m, key('5'))
	if m.activeTab != tabCPU {
		t.Error("tab switched while help open")
	}

	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.showHelp {
		t.Error("esc did not close help")
	}
}

func TestTabJumpAndCycle(t *testing.T) {
	m := testModel()
	m, _ = press(t, m, key('5'))
	if m.activeTab != tabNet {
		t.Errorf("activeTab = %v, want net", m.activeTab)
	}
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabPower {
		t.Errorf("activeTab = %v, want power", m.activeTab)
	}
}

func TestConnStateFilterCycle(t *testing.T) {
	m := testModel()
	m.activeTab = tabNet
	m.conns = []probe.ConnInfo{
		{Proto: "TCP", LocalAddr: "0.0.0.0:22", Status: "LISTEN"},
		{Proto: "TCP", LocalAddr: "10.0.0.1:5000", RemoteAddr: "10.0.0.2:80", Status: "ESTABLISHED"},
		{Proto: "UDP", LocalAddr: "0.0.0.0:68", Status: "NONE"},
	}

	m, _ = press(t, m, key('f'))
	if m.connFilter != connListen {
		t.Fatalf("connFilter = %v, want LISTEN", m.connFilter)
	}
	text := m.renderNetText()
	if strings.Contains(text, "ESTABLISHED") || strings.Contains(text, "NONE") {
		t.Errorf("LISTEN filter still shows other states:\n%s", text)
	}

	m, _ = press(t, m, key('f'))
	if m.connFilter != connEstab {
		t.Fatalf("connFilter = %v, want ESTABLISHED", m.connFilter)
	}
	m, _ = press(t, m, key('f'))
	if m.connFilter != connAll {
		t.Fatalf("connFilter = %v, want ALL (wrap)", m.connFilter)
	}

	line := m.connSummaryLine()
	if !strings.Contains(line, "3 total") || !strings.Contains(line, "1 listen") {
		t.Errorf("summary line = %q", line)
	}
}

func TestConnStateMatch(t *testing.T) {
	if !connAll.match("whatever") {
		t.Error("ALL must match everything")
	}
	if !connListen.match("LISTEN") || connListen.match("ESTABLISHED") {
		t.Error("LISTEN filter wrong")
	}
	if !connEstab.match("ESTABLISHED") || connEstab.match("NONE") {
		t.Error("ESTABLISHED filter wrong")
	}
}

func TestProcsFilterMatchesCmdline(t *testing.T) {
	m := testModel()
	m.procs = []probe.ProcInfo{
		{PID: 1, Name: "python3", CmdLine: "python3 /srv/portal-sync.py"},
		{PID: 2, Name: "sshd", CmdLine: "sshd: vlad [priv]"},
	}

	m.procsQuery = "portal-sync"
	view := m.computeProcsView()
	if len(view) != 1 || view[0].PID != 1 {
		t.Fatalf("cmdline filter failed: %+v", view)
	}

	m.procsQuery = "nothing-matches"
	if view := m.computeProcsView(); len(view) != 0 {
		t.Errorf("expected no matches, got %+v", view)
	}
}

func TestProcDetailOverlayKeys(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{{PID: 4242, Name: "demo", CPUPct: 1}}
	m.refreshProcs()

	m, cmd := press(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.detail == nil {
		t.Fatal("enter did not open the detail overlay")
	}
	if cmd == nil {
		t.Fatal("opening detail should fetch it")
	}
	if m.detail.pid != 4242 {
		t.Errorf("detail pid = %d", m.detail.pid)
	}

	// Simulate the fetch landing.
	m, _ = press(t, m, procDetailMsg{d: probe.ProcDetail{PID: 4242, Name: "demo", Nice: 0, Threads: 3}})
	if !m.detail.loaded {
		t.Fatal("detail not marked loaded")
	}
	if !strings.Contains(m.View(), "Process 4242") {
		t.Error("detail view not rendered")
	}

	// Keys are scoped to the overlay: tab switching is swallowed.
	m, _ = press(t, m, key('5'))
	if m.activeTab != tabProc {
		t.Error("tab switched while detail open")
	}

	// k arms the kill prompt for the detail pid.
	m, _ = press(t, m, key('k'))
	if m.procsKill == nil || m.procsKill.pid != 4242 || m.procsKill.hard {
		t.Fatalf("kill prompt = %+v", m.procsKill)
	}
	// esc cancels the prompt but keeps the overlay.
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.procsKill != nil {
		t.Error("esc did not cancel kill prompt")
	}
	if m.detail == nil {
		t.Error("cancelling the kill prompt should keep the overlay")
	}

	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.detail != nil {
		t.Error("esc did not close the overlay")
	}
}

func TestLatencyMsgUpdatesWindows(t *testing.T) {
	m := testModel()
	m, _ = press(t, m, latencyMsg{
		{Target: "gateway", Addr: "192.168.1.1", OK: true, RTT: 4 * time.Millisecond, Method: "icmp"},
		{Target: "1.1.1.1", Addr: "1.1.1.1", OK: false, Method: "icmp"},
	})
	if len(m.lossWin["gateway"]) != 1 || !m.lossWin["gateway"][0] {
		t.Errorf("gateway window = %v", m.lossWin["gateway"])
	}
	if len(m.rttHist["gateway"]) != 1 || m.rttHist["gateway"][0] != 4 {
		t.Errorf("gateway rtt hist = %v", m.rttHist["gateway"])
	}
	if len(m.rttHist["1.1.1.1"]) != 0 {
		t.Error("lost pings must not enter the RTT history")
	}
	out := m.renderLatency()
	if !strings.Contains(out, "gateway 192.168.1.1") || !strings.Contains(out, "lost") {
		t.Errorf("latency render = %q", out)
	}
}

func TestGovernorKeyWithoutCpufreq(t *testing.T) {
	m := testModel()
	m.activeTab = tabCPU
	m, _ = press(t, m, key('g'))
	if m.notice == "" {
		t.Error("g without cpufreq policies should set a notice")
	}
}
