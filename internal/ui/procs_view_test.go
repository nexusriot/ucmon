package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/probe"
)

func treeFixture() []probe.ProcInfo {
	return []probe.ProcInfo{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 4, PPID: 1, Name: "daemon"},
		{PID: 2, PPID: 1, Name: "shell"},
		{PID: 3, PPID: 2, Name: "child"},
	}
}

func TestComputeProcsTreeOrderAndPrefixes(t *testing.T) {
	m := testModel()
	m.procs = treeFixture()
	m.procsSort = sortPID

	rows, prefixes := m.computeProcsTree()
	if len(rows) != 4 || len(prefixes) != 4 {
		t.Fatalf("rows/prefixes = %d/%d", len(rows), len(prefixes))
	}

	wantPIDs := []int32{1, 2, 3, 4} // DFS: init → shell → child, then daemon
	for i, w := range wantPIDs {
		if rows[i].PID != w {
			t.Fatalf("row %d = pid %d, want %d (order %v)", i, rows[i].PID, w, rows)
		}
	}

	if prefixes[0] != "" {
		t.Errorf("root prefix = %q", prefixes[0])
	}
	if prefixes[1] != "├─ " {
		t.Errorf("first child prefix = %q", prefixes[1])
	}
	if prefixes[2] != "│  └─ " {
		t.Errorf("grandchild prefix = %q", prefixes[2])
	}
	if prefixes[3] != "└─ " {
		t.Errorf("last child prefix = %q", prefixes[3])
	}
}

func TestComputeProcsTreeSelfParent(t *testing.T) {
	m := testModel()
	// PID 2's parent is itself — must be treated as a root, not recursed.
	m.procs = []probe.ProcInfo{
		{PID: 1, PPID: 0, Name: "init"},
		{PID: 2, PPID: 2, Name: "weird"},
	}
	m.procsSort = sortPID

	rows, _ := m.computeProcsTree()
	if len(rows) != 2 {
		t.Fatalf("self-parented process lost: %+v", rows)
	}
}

func TestTreeToggleAndSortKeys(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = treeFixture()
	m.refreshProcs()

	if m.procsSort != sortCPU {
		t.Fatalf("default sort = %v", m.procsSort)
	}
	m, _ = press(t, m, key('s'))
	if m.procsSort != sortMem {
		t.Errorf("s did not cycle sort, got %v", m.procsSort)
	}

	m, _ = press(t, m, key('t'))
	if !m.procsTree {
		t.Fatal("t did not enable tree view")
	}
	if len(m.procsPrefix) != len(m.procsView) {
		t.Errorf("tree prefixes not aligned: %d vs %d", len(m.procsPrefix), len(m.procsView))
	}
	if !strings.Contains(m.renderProcsText(), "└─") {
		t.Error("tree glyphs missing from rendered text")
	}

	m, _ = press(t, m, key('t'))
	if m.procsTree {
		t.Error("t did not toggle tree off")
	}
}

func TestProcSearchFlow(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = treeFixture()
	m.refreshProcs()

	m, _ = press(t, m, key('/'))
	if !m.procsSearching {
		t.Fatal("/ did not enter search mode")
	}

	// Type "shell" and apply.
	for _, r := range "shell" {
		m, _ = press(t, m, key(r))
	}
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.procsSearching {
		t.Fatal("enter did not leave search mode")
	}
	if m.procsQuery != "shell" {
		t.Fatalf("query = %q", m.procsQuery)
	}
	if len(m.procsView) != 1 || m.procsView[0].Name != "shell" {
		t.Errorf("filtered view = %+v", m.procsView)
	}

	// ctrl+u clears the applied filter.
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyCtrlU})
	if m.procsQuery != "" || len(m.procsView) != 4 {
		t.Errorf("ctrl+u: query=%q view=%d", m.procsQuery, len(m.procsView))
	}

	// esc cancels search mode without applying.
	m, _ = press(t, m, key('/'))
	m, _ = press(t, m, key('x'))
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.procsSearching || m.procsQuery != "" {
		t.Errorf("esc: searching=%v query=%q", m.procsSearching, m.procsQuery)
	}
}

func TestKillPromptArmAndCancel(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = treeFixture()
	m.refreshProcs()

	m, _ = press(t, m, key('k'))
	if m.procsKill == nil || m.procsKill.hard {
		t.Fatalf("k should arm a SIGTERM prompt, got %+v", m.procsKill)
	}
	if m.procsKill.pid != m.procsView[0].PID {
		t.Errorf("prompt pid = %d, want selected %d", m.procsKill.pid, m.procsView[0].PID)
	}

	// Any key but y/Y/enter cancels.
	m, _ = press(t, m, key('n'))
	if m.procsKill != nil {
		t.Error("n did not cancel the prompt")
	}

	m, _ = press(t, m, key('K'))
	if m.procsKill == nil || !m.procsKill.hard {
		t.Fatalf("K should arm a SIGKILL prompt, got %+v", m.procsKill)
	}
	if !strings.Contains(m.View(), "SIGKILL") {
		t.Error("prompt not rendered")
	}
	m, _ = press(t, m, tea.KeyMsg{Type: tea.KeyEscape})
	if m.procsKill != nil {
		t.Error("esc did not cancel the prompt")
	}
}

func TestSelectedProcBounds(t *testing.T) {
	m := testModel()
	if _, ok := m.selectedProc(); ok {
		t.Error("empty view should have no selection")
	}
	m.procs = treeFixture()
	m.refreshProcs()
	m.procsSel = 99
	m.refreshProcs() // clamps
	if p, ok := m.selectedProc(); !ok || p.PID == 0 {
		t.Errorf("selection not clamped: %+v ok=%v", p, ok)
	}
}

func TestProcSortStrings(t *testing.T) {
	want := map[procSort]string{sortCPU: "CPU", sortMem: "MEM", sortPID: "PID", sortName: "NAME"}
	for s, w := range want {
		if s.String() != w {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), w)
		}
	}
}

// End-to-end guard for the highlight crash: a process name longer than the
// NAME column gets a "…" from trunc, and rendering that row under an active
// filter used to panic inside highlightFold. gopsutil reports whole words for
// Status ("sleep", "idle"), so the row ends flush with no padding after it —
// a filter matching that last character is what pushed the byte-indexed
// search past the end of the rune slice.
func TestProcViewRendersFilteredLongNames(t *testing.T) {
	m := testModel()
	m.activeTab = tabProc
	m.procs = []probe.ProcInfo{
		{PID: 4242, Name: "org.gnome.Shell.Extensions.Foo.Bar", User: "vlad",
			Status: "sleep", CmdLine: "/usr/bin/python3 -m gnome.shell.ext"},
		{PID: 4243, Name: "日本語のプロセス名がとても長い場合のテストです", User: "vlad",
			Status: "idle", CmdLine: "/usr/bin/example --long-flag"},
		{PID: 4244, Name: "sshd", User: "root", Status: "running", CmdLine: "sshd: /usr/sbin/sshd"},
	}

	for _, q := range []string{"p", "e", "g", "n", "s", "S", "4", "root", "日本", "python"} {
		m.procsQuery = q
		m.refreshProcs()
		if out := m.View(); out == "" {
			t.Errorf("query %q rendered nothing", q)
		}
	}
}

// Same path on the Network tab, where the connection table truncates
// addresses and process names.
func TestNetViewRendersFilteredLongNames(t *testing.T) {
	m := testModel()
	m.activeTab = tabNet
	m.conns = []probe.ConnInfo{
		{Proto: "TCP", LocalAddr: "192.168.88.10:22", RemoteAddr: "10.0.0.1:54321",
			Status: "ESTABLISHED", PID: 42, Process: strings.Repeat("very-long-proc-", 6)},
		{Proto: "TCP6", LocalAddr: "[::]:443", RemoteAddr: "-", Status: "LISTEN", PID: 1, Process: "nginx"},
	}

	for _, q := range []string{"1", "l", "LISTEN", "very", "-"} {
		m.netQuery = q
		m.netText = m.renderNetText()
		m.netVP.SetContent(hardClipLinesToWidth(m.netText, m.netVP.Width))
		if out := m.View(); out == "" {
			t.Errorf("query %q rendered nothing", q)
		}
	}
}
