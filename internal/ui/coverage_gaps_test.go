package ui

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// ansiSafeTruncate must count only printable width, passing escape sequences
// through whole. Its OSC and lone-ESC branches guard against a truncation
// landing mid-sequence and corrupting every following row.
func TestAnsiSafeTruncateEscapes(t *testing.T) {
	const (
		sgr = "\x1b[31m"     // CSI colour
		rst = "\x1b[0m"      // CSI reset
		osc = "\x1b]0;t\x07" // OSC terminated by BEL
		st  = "\x1b]8;;u\x1b\\"
	)
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"csi is zero width", sgr + "abc" + rst, 3, sgr + "abc" + rst},
		{"csi truncates payload only", sgr + "abcdef" + rst, 3, sgr + "abc"},
		{"osc bel is zero width", osc + "abc", 3, osc + "abc"},
		{"osc st is zero width", st + "abc", 3, st + "abc"},
		{"lone esc pair passes", "\x1bMabc", 3, "\x1bMabc"},
		{"trailing lone esc", "abc\x1b", 3, "abc\x1b"},
		{"zero width returns empty", sgr + "abc", 0, ""},
		{"negative width returns empty", "abc", -1, ""},
		// CJK is double-width, so a 2-column budget fits exactly one rune and
		// a 3-column budget must not overflow into a second.
		{"wide rune fills its columns", "日本語", 4, "日本"},
		{"wide rune never overflows", "日本語", 3, "日"},
		{"invalid utf8 skipped", "a\xffb", 2, "ab"},
	}
	for _, c := range cases {
		if got := ansiSafeTruncate(c.in, c.width); got != c.want {
			t.Errorf("%s: ansiSafeTruncate(%q, %d) = %q, want %q",
				c.name, c.in, c.width, got, c.want)
		}
	}
}

// The cursor must stay on screen whichever direction it moved, and a zero-height
// viewport must not be scrolled at all (it has no rows to scroll).
func TestEnsureProcVisible(t *testing.T) {
	cases := []struct {
		name        string
		height, sel int
		offset      int
		wantOffset  int
	}{
		{"already visible", 10, 5, 0, 0},
		{"cursor above window", 10, 3, 8, 3},
		{"cursor below window", 10, 25, 0, 16},
		{"cursor at last visible row", 10, 9, 0, 0},
		{"cursor one past window", 10, 10, 0, 1},
		{"zero height is left alone", 0, 50, 7, 7},
		{"negative height is left alone", -1, 50, 7, 7},
	}
	for _, c := range cases {
		m := testModel()
		for i := 0; i < 100; i++ {
			m.procs = append(m.procs, probe.ProcInfo{PID: int32(i + 1), Name: "p", Status: "sleep"})
		}
		m.refreshProcs()
		m.procsVP.Height = c.height
		m.procsVP.SetYOffset(c.offset)
		m.procsSel = c.sel

		m.ensureProcVisible()
		if got := m.procsVP.YOffset; got != c.wantOffset {
			t.Errorf("%s: YOffset = %d, want %d", c.name, got, c.wantOffset)
		}
	}
}

// Init primes every reading once. The command count is the contract: dropping
// one silently leaves a tab blank until its first scheduled tick.
func TestInitDispatchesEveryProbe(t *testing.T) {
	batchLen := func(cmd tea.Cmd) int {
		if cmd == nil {
			return 0
		}
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			return 1
		}
		return len(batch)
	}

	withPing := NewModel(config.Default())
	if got := batchLen(withPing.Init()); got != 11 {
		t.Errorf("Init dispatched %d commands with ping enabled, want 11", got)
	}

	cfg := config.Default()
	cfg.PingEnabled = false
	noPing := NewModel(cfg)
	if got := batchLen(noPing.Init()); got != 10 {
		t.Errorf("Init dispatched %d commands with ping disabled, want 10", got)
	}
	if noPing.latSampler != nil {
		t.Error("ping_enabled = false still built a latency sampler")
	}
}

func TestVersionMatchesConst(t *testing.T) {
	if Version() != version {
		t.Errorf("Version() = %q, const version = %q", Version(), version)
	}
	if !strings.Contains(Version(), ".") {
		t.Errorf("Version() = %q, want a dotted release", Version())
	}
}

func TestConnStateString(t *testing.T) {
	want := map[connState]string{connAll: "ALL", connListen: "LISTEN", connEstab: "ESTABLISHED"}
	for c, w := range want {
		if got := c.String(); got != w {
			t.Errorf("connState(%d).String() = %q, want %q", c, got, w)
		}
	}
	if got := connState(99).String(); got != "ALL" {
		t.Errorf("unknown connState = %q, want the ALL fallback", got)
	}
}

func TestKillPromptSignal(t *testing.T) {
	if got := (killPrompt{hard: false}).signal(); got != "SIGTERM" {
		t.Errorf("soft kill signal = %q", got)
	}
	if got := (killPrompt{hard: true}).signal(); got != "SIGKILL" {
		t.Errorf("hard kill signal = %q", got)
	}
}

// The overlay title falls back to the list row's name until the detail fetch
// lands, so it is never blank.
func TestProcDetailDisplayName(t *testing.T) {
	st := &procDetailState{name: "from-list"}
	if got := st.displayName(); got != "from-list" {
		t.Errorf("unloaded displayName = %q", got)
	}

	st.loaded = true
	if got := st.displayName(); got != "from-list" {
		t.Errorf("loaded but empty detail name = %q, want the list name", got)
	}

	st.d.Name = "from-detail"
	if got := st.displayName(); got != "from-detail" {
		t.Errorf("loaded displayName = %q", got)
	}
}

// Every fetch command must return its own message type on a healthy host.
// Running them is also the cheapest guard against a gopsutil bump changing a
// signature or starting to error: the tab would silently show "Collecting…"
// forever otherwise.
func TestFetchCommandsReturnTheirMessage(t *testing.T) {
	m := testModel()
	cases := []struct {
		name string
		cmd  tea.Cmd
		want any
	}{
		{"cpu", fetchCPUCmd(), cpuMsg{}},
		{"mem", fetchMemCmd(), memMsg{}},
		{"power", fetchPowerCmd(), powerMsg{}},
		{"psi", fetchPSICmd(), psiMsg{}},
		{"disk", m.fetchDiskCmd(), diskMsg{}},
		{"net", fetchNetCmd(m.netSampler), netMsg{}},
		{"conns", fetchConnsCmd(), connMsg{}},
		{"procs", m.fetchProcsCmd(), procMsg{}},
		{"sys", m.fetchSysCmd(), sysMsg{}},
		{"detail", fetchProcDetailCmd(int32(os.Getpid())), procDetailMsg{}},
	}
	for _, c := range cases {
		if c.cmd == nil {
			t.Errorf("%s: no command built", c.name)
			continue
		}
		msg := c.cmd()
		if _, isErr := msg.(errMsg); isErr {
			t.Logf("%s: probe errored on this host (%v)", c.name, msg)
			continue
		}
		if reflect.TypeOf(msg) != reflect.TypeOf(c.want) {
			t.Errorf("%s: returned %T, want %T", c.name, msg, c.want)
		}
	}
}

// The tick keeps its own heartbeat: the command must actually fire and carry a
// timestamp, or sampling stops after the first frame.
func TestTickEveryFires(t *testing.T) {
	msg := tickEvery(time.Millisecond)()
	ts, ok := msg.(tickMsg)
	if !ok {
		t.Fatalf("tickEvery produced %T, want tickMsg", msg)
	}
	if time.Time(ts).IsZero() {
		t.Error("tickMsg carries a zero time")
	}
}

// A bad alert_log_cap or alert_clear_ticks must not disable the engine.
func TestNewAlertEngineClampsItsBounds(t *testing.T) {
	for _, c := range []struct {
		capN, ticks        int
		wantCap, wantTicks int
	}{
		{0, 0, 16, 1},
		{-5, -5, 16, 1},
		{15, 0, 16, 1},
		{200, 3, 200, 3},
	} {
		e := newAlertEngine(c.capN, c.ticks)
		if e.cap != c.wantCap || e.clearTicks != c.wantTicks {
			t.Errorf("newAlertEngine(%d,%d) = cap %d ticks %d, want cap %d ticks %d",
				c.capN, c.ticks, e.cap, e.clearTicks, c.wantCap, c.wantTicks)
		}
		if e.active == nil {
			t.Error("active map not initialised")
		}
	}
}

// The Alerts tab lists criticals first, then alphabetically, so the order does
// not jitter between renders of the same set.
func TestActiveListOrdering(t *testing.T) {
	e := newAlertEngine(16, 1)
	e.update(map[string]alertCond{
		"b": {sevWarn, "warn-b"},
		"a": {sevWarn, "warn-a"},
		"d": {sevCrit, "crit-d"},
		"c": {sevCrit, "crit-c"},
	}, nonZeroTime())

	got := e.activeList()
	want := []string{"crit-c", "crit-d", "warn-a", "warn-b"}
	if len(got) != len(want) {
		t.Fatalf("activeList len = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].msg != w {
			t.Errorf("activeList[%d] = %q, want %q", i, got[i].msg, w)
		}
	}
}
