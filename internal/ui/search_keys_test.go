package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/probe"
)

// A focused search box must receive digits: the placeholder invites a PID, and
// the global map used to route 1-8 to the tab jumps instead.
func TestProcSearchAcceptsDigits(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{{PID: 4242, Name: "sshd"}, {PID: 7, Name: "ksoftirqd"}}
	m.refreshProcs()

	m, _ = press(t, m, key('/'))
	for _, r := range "4242" {
		m, _ = press(t, m, key(r))
	}
	if got := m.procsSearch.Value(); got != "4242" {
		t.Errorf("search box = %q, want %q", got, "4242")
	}
	if m.activeTab != tabProc {
		t.Errorf("tab changed to %v while typing", m.activeTab)
	}
	if !m.procsSearching {
		t.Error("digits ended search mode")
	}

	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.procsView) != 1 || m.procsView[0].PID != 4242 {
		t.Errorf("filtering by pid gave %+v", m.procsView)
	}
}

// Arrows, tab and "/" belong to the text input while it is focused.
func TestSearchBoxOwnsNavigationKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		tab  tab
		msg  tea.KeyMsg
	}{
		{"proc left", tabProc, tea.KeyMsg{Type: tea.KeyLeft}},
		{"proc right", tabProc, tea.KeyMsg{Type: tea.KeyRight}},
		{"proc tab", tabProc, tea.KeyMsg{Type: tea.KeyTab}},
		{"net left", tabNet, tea.KeyMsg{Type: tea.KeyLeft}},
		{"net tab", tabNet, tea.KeyMsg{Type: tea.KeyTab}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := testModel()
			m.activeTab = c.tab
			m, _ = press(t, m, key('/'))
			m, _ = press(t, m, c.msg)
			if m.activeTab != c.tab {
				t.Errorf("tab switched to %v while the search box was focused", m.activeTab)
			}
		})
	}
}

// "/" typed inside the box is a literal, not a re-open that wipes the value.
func TestSlashInsideSearchBoxIsLiteral(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc

	m, _ = press(t, m, key('/'))
	for _, r := range "usr/bin" {
		m, _ = press(t, m, key(r))
	}
	if got := m.procsSearch.Value(); got != "usr/bin" {
		t.Errorf("search box = %q, want %q", got, "usr/bin")
	}
}

// ctrl+c must still quit from inside a search box.
func TestSearchBoxStillQuits(t *testing.T) {
	m := testModel()
	m.activeTab = tabNet
	m, _ = press(t, m, key('/'))
	_, cmd := press(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c did not quit, got %T", cmd())
	}
}

// Cycling into the Alerts tab must show the same content the 8 key does.
func TestAlertsContentFreshOnTabCycle(t *testing.T) {
	m := testModel()
	m.alerts.update(condsOf("temp:x"), time.Now())

	m.activeTab = tabGPU
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyTab})
	if m.activeTab != tabAlerts {
		t.Fatalf("tab = %v, want alerts", m.activeTab)
	}
	if !strings.Contains(m.alertsVP.View(), "temp:x") {
		t.Errorf("alerts arrived stale via tab cycle:\n%s", m.alertsVP.View())
	}
}

// The viewport's own j/d/u/b/f bindings must not scroll the process list away
// from the cursor; ensureProcVisible would snap it back a tick later.
func TestProcListIgnoresViewportScrollKeys(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	for i := 0; i < 100; i++ {
		m.procs = append(m.procs, probe.ProcInfo{PID: int32(i + 1), Name: "p"})
	}
	m.refreshProcs()

	for _, r := range []rune{'j', 'd', 'u', 'b'} {
		m, _ = press(t, m, key(r))
		if m.procsVP.YOffset != 0 {
			t.Errorf("%q scrolled the viewport to %d without moving the cursor", r, m.procsVP.YOffset)
		}
		if m.procsSel != 0 {
			t.Errorf("%q moved the cursor to %d", r, m.procsSel)
		}
	}

	// The explicit cursor keys still work.
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyDown})
	if m.procsSel != 1 {
		t.Errorf("down: cursor = %d, want 1", m.procsSel)
	}
}
