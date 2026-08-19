package ui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/probe"
)

func detailModel(t *testing.T, pid int32) Model {
	t.Helper()
	m := testModel()
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{{PID: pid, Name: "target", User: "vlad", Status: "sleep"}}
	m.refreshProcs()
	m.detail = &procDetailState{pid: pid, name: "target", loaded: true,
		d: probe.ProcDetail{PID: pid, Name: "target", Nice: 0}}
	return m
}

// The overlay owns every key while it is open — the global map must not see
// tab switches, digits or the snapshot key, or the user would navigate away
// from a modal they cannot see they are in.
func TestProcDetailSwallowsGlobalKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyTab},
		{Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune("1")},
		{Type: tea.KeyRunes, Runes: []rune("5")},
		{Type: tea.KeyRunes, Runes: []rune("w")},
		{Type: tea.KeyRunes, Runes: []rune("?")},
		{Type: tea.KeyRunes, Runes: []rune("/")},
		{Type: tea.KeySpace},
	} {
		m := detailModel(t, 1)
		m, _ = press(t, m, k)

		if m.activeTab != tabProc {
			t.Errorf("%v switched tab to %v", k, m.activeTab)
		}
		if m.detail == nil {
			t.Errorf("%v closed the overlay", k)
		}
		if m.showHelp {
			t.Errorf("%v opened the help overlay over the detail overlay", k)
		}
		if m.procsSearching {
			t.Errorf("%v opened the search box under the overlay", k)
		}
		if m.paused {
			t.Errorf("%v paused sampling from inside the overlay", k)
		}
	}
}

func TestProcDetailCloseKeys(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEscape},
		{Type: tea.KeyRunes, Runes: []rune("q")},
		{Type: tea.KeyEnter},
	} {
		m := detailModel(t, 1)
		m, _ = press(t, m, k)
		if m.detail != nil {
			t.Errorf("%v did not close the overlay", k)
		}
	}
}

// k/K arm the shared kill prompt against the overlay's pid, not the list cursor.
func TestProcDetailArmsKillForItsOwnPID(t *testing.T) {
	for _, c := range []struct {
		key  rune
		hard bool
	}{{'k', false}, {'K', true}} {
		m := detailModel(t, 4242)
		m.procs = append(m.procs, probe.ProcInfo{PID: 1, Name: "other", Status: "sleep"})
		m.refreshProcs()

		m, _ = press(t, m, key(c.key))
		if m.procsKill == nil {
			t.Fatalf("%q did not arm a kill prompt", c.key)
		}
		if m.procsKill.pid != 4242 {
			t.Errorf("%q armed pid %d, want the overlay's 4242", c.key, m.procsKill.pid)
		}
		if m.procsKill.hard != c.hard {
			t.Errorf("%q hard = %v, want %v", c.key, m.procsKill.hard, c.hard)
		}
		if m.detail == nil {
			t.Errorf("%q closed the overlay", c.key)
		}
	}
}

func TestProcDetailCtrlCQuits(t *testing.T) {
	m := detailModel(t, 1)
	_, cmd := press(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c did not quit, got %T", cmd())
	}
}

// Renice targets this test process, where raising niceness is always allowed
// and lowering it is refused without CAP_SYS_NICE — both branches are real.
func TestReniceDetailOnSelf(t *testing.T) {
	self := int32(os.Getpid())
	m := detailModel(t, self)

	if cmd := m.reniceDetail(1); cmd == nil {
		t.Fatal("raising niceness on ourselves returned no refresh command")
	}
	if n := m.detail.notice; n == "" {
		t.Error("renice set no status line")
	}

	// An unloaded overlay has no nice value to adjust yet.
	m2 := detailModel(t, self)
	m2.detail.loaded = false
	if cmd := m2.reniceDetail(1); cmd != nil {
		t.Error("renice before the detail loaded should do nothing")
	}
}

// Signalling a pid that cannot exist exercises the failure notice without
// touching any real process.
func TestProcDetailSignalFailureIsReported(t *testing.T) {
	for _, k := range []rune{'p', 'c'} {
		m := detailModel(t, 0x7FFFFFFE)
		m, _ = press(t, m, key(k))
		if m.detail == nil {
			t.Fatalf("%q closed the overlay", k)
		}
		if m.detail.notice == "" {
			t.Errorf("%q set no status line for an impossible pid", k)
		}
	}
}

// Every optional row in the detail overlay is conditional, so a fully populated
// process is the only way to render them all. Fields that need permissions
// degrade to their zero value and must simply be omitted.
func TestViewProcDetailRendersEveryRow(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.detail = &procDetailState{pid: 4242, name: "target", loaded: true, d: probe.ProcDetail{
		PID: 4242, Name: "gnome-shell", User: "vlad", Status: "running",
		CmdLine: "/usr/bin/gnome-shell --replace --sm-disable",
		Exe:     "/usr/bin/gnome-shell", Cwd: "/home/vlad",
		Nice: 3, Threads: 12, FDs: 64, Children: 5,
		MemRSS: 1 << 27, MemVMS: 1 << 31, MemSwap: 1 << 20,
		CreateTime: 1_700_000_000_000,
		IORead:     1 << 24, IOWrite: 1 << 22, IOOK: true,
		Conns: []probe.ConnInfo{
			{Proto: "TCP", LocalAddr: "10.0.0.1:5555", RemoteAddr: "10.0.0.2:443", Status: "ESTABLISHED"},
		},
	}}

	out := m.viewProcDetail()
	for _, want := range []string{
		"4242", "gnome-shell", "vlad", "running", "started",
		"nice", "threads", "open fds", "children",
		"rss", "vms", "swap", "disk io", "exe", "cwd", "cmdline", "sockets",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail overlay is missing %q", want)
		}
	}

	// More sockets than fit are summarised rather than dropped silently.
	for i := 0; i < 12; i++ {
		m.detail.d.Conns = append(m.detail.d.Conns, probe.ConnInfo{
			Proto: "UDP", LocalAddr: "0.0.0.0:0", RemoteAddr: "-", Status: "NONE"})
	}
	if out := m.viewProcDetail(); !strings.Contains(out, "more") {
		t.Error("a truncated socket list does not say how many were hidden")
	}

	// Unreadable fields (FDs = -1, no IO counters) are omitted, not shown as 0.
	m.detail.d.FDs = -1
	m.detail.d.IOOK = false
	m.detail.d.Children = 0
	m.detail.d.Cwd = ""
	out = m.viewProcDetail()
	for _, absent := range []string{"open fds", "disk io", "children", "cwd"} {
		if strings.Contains(out, absent) {
			t.Errorf("%q rendered despite being unavailable", absent)
		}
	}
}

// Until the fetch lands the overlay says so rather than showing empty rows.
func TestViewProcDetailLoadingState(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.detail = &procDetailState{pid: 7, name: "waiting"}

	out := m.viewProcDetail()
	if !strings.Contains(out, "Loading") {
		t.Errorf("unloaded overlay = %q", out)
	}
	if !strings.Contains(out, "waiting") {
		t.Error("the list row's name is not shown while loading")
	}
}
