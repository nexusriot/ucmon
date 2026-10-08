package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The sampler must keep the previous refresh's raw counters — that is what
// makes CPU% an interval rate instead of a lifetime average.
func TestProcSamplerKeepsPreviousSample(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("first List: %v", err)
	}

	self := int32(os.Getpid())
	first, ok := s.prev[self]
	if !ok {
		t.Fatalf("own pid %d not tracked after List", self)
	}
	if first.startTime == 0 {
		t.Error("start time not recorded; PID reuse could not be detected")
	}
	if first.at.IsZero() {
		t.Error("sample time not recorded; CPU%% would have no interval to divide by")
	}

	if _, err := s.List(); err != nil {
		t.Fatalf("second List: %v", err)
	}
	second, ok := s.prev[self]
	if !ok {
		t.Fatalf("own pid %d dropped on second List", self)
	}
	if !second.at.After(first.at) {
		t.Error("sample timestamp was not advanced between refreshes")
	}
	if second.startTime != first.startTime {
		t.Error("start time changed for a process that did not restart")
	}
}

func TestProcSamplerEvictsExitedPIDs(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("List: %v", err)
	}

	// Inject an impossible PID and make sure the next refresh drops it.
	s.prev[-4242] = procSample{}
	if _, err := s.List(); err != nil {
		t.Fatalf("List: %v", err)
	}
	if _, ok := s.prev[-4242]; ok {
		t.Error("stale pid survived eviction")
	}
}

// A refresh must describe this very process correctly: the walk is the one
// place where a parsing slip would show up as a whole list of wrong numbers.
func TestProcSamplerDescribesSelf(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) { // burn a measurable slice of one core
	}

	procs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	self := int32(os.Getpid())
	for _, p := range procs {
		if p.PID != self {
			continue
		}
		if p.CPUPct < 50 {
			t.Errorf("busy-looped for 250ms, CPU%% = %.1f; interval rate is not being computed", p.CPUPct)
		}
		if p.MemRSS == 0 {
			t.Error("RSS is zero for a running Go test binary")
		}
		if p.MemPct <= 0 {
			t.Error("MemPct is zero; MemTotal was not read")
		}
		if p.User == "" {
			t.Error("User is empty; the uid lookup failed")
		}
		if p.PPID <= 0 {
			t.Errorf("PPID = %d", p.PPID)
		}
		if p.Status == "" {
			t.Error("Status is empty")
		}
		return
	}
	t.Fatal("own pid missing from the process list")
}

// A recycled PID restarts its counters, so a delta against the previous
// process's numbers would report a nonsense rate. startTime settles identity.
func TestProcSamplerIgnoresRecycledPID(t *testing.T) {
	s := NewProcSampler()
	if _, err := s.List(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	self := int32(os.Getpid())
	stale := s.prev[self]
	stale.startTime++  // pretend a different process now holds this PID
	stale.cpuTicks = 0 // ... with counters that start low
	stale.at = time.Now().Add(-time.Hour)
	s.prev[self] = stale

	procs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, p := range procs {
		if p.PID != self {
			continue
		}
		if p.CPUPct != 0 {
			t.Errorf("CPU%% = %.3f for a PID whose identity changed; want a fresh 0 sample", p.CPUPct)
		}
		return
	}
	t.Fatal("own pid missing from the process list")
}

func TestParseProcStat(t *testing.T) {
	// A comm containing both spaces and parentheses is the case that breaks a
	// naive field split, and real processes do it (e.g. "(sd-pam)").
	line := []byte("1234 (we ird (proc)) S 42 1234 1234 0 -1 4194304 " +
		"100 200 0 0 " + // minflt cminflt majflt cmajflt
		"777 333 0 0 " + // utime stime cutime cstime
		"20 0 7 0 " + // priority nice num_threads itrealvalue
		"98765 " + // starttime
		"104857600 " + // vsize
		"512 " + // rss (pages)
		"18446744073709551615 rest ignored")

	st, ok := parseProcStat(line)
	if !ok {
		t.Fatal("parseProcStat rejected a well-formed line")
	}
	if st.comm != "we ird (proc)" {
		t.Errorf("comm = %q, want %q", st.comm, "we ird (proc)")
	}
	if st.state != "S" {
		t.Errorf("state = %q, want S", st.state)
	}
	if st.ppid != 42 {
		t.Errorf("ppid = %d, want 42", st.ppid)
	}
	if st.utime != 777 || st.stime != 333 {
		t.Errorf("utime/stime = %d/%d, want 777/333", st.utime, st.stime)
	}
	if st.startTime != 98765 {
		t.Errorf("startTime = %d, want 98765", st.startTime)
	}
	if st.rssPages != 512 {
		t.Errorf("rssPages = %d, want 512", st.rssPages)
	}
}

func TestParseProcStatRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		[]byte(""),
		[]byte("no parens here"),
		[]byte("1 (short) S 0 0"), // truncated before the rss column
	}
	for _, c := range cases {
		if _, ok := parseProcStat(c); ok {
			t.Errorf("parseProcStat(%q) accepted a malformed line", c)
		}
	}
}

func TestReadProcIO(t *testing.T) {
	dir := t.TempDir()
	body := "rchar: 123\nwchar: 456\nsyscr: 7\nsyscw: 8\nread_bytes: 4096\nwrite_bytes: 8192\ncancelled_write_bytes: 0\n"
	if err := os.WriteFile(filepath.Join(dir, "io"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r, w, ok := readProcIO(dir)
	if !ok {
		t.Fatal("readProcIO reported the file as unreadable")
	}
	if r != 4096 || w != 8192 {
		t.Errorf("read/write = %d/%d, want 4096/8192", r, w)
	}

	// An unreadable io file (another user's process) must report "unknown"
	// rather than a confident zero.
	if _, _, ok := readProcIO(t.TempDir()); ok {
		t.Error("a missing io file reported as readable")
	}
}

func TestReadMemTotal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meminfo")
	if err := os.WriteFile(path, []byte("MemFree:  100 kB\nMemTotal: 2048 kB\nBuffers: 1 kB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := meminfoPath
	meminfoPath = path
	defer func() { meminfoPath = orig }()

	if got := readMemTotal(); got != 2048*1024 {
		t.Errorf("readMemTotal() = %d, want %d", got, 2048*1024)
	}

	meminfoPath = filepath.Join(dir, "absent")
	if got := readMemTotal(); got != 0 {
		t.Errorf("readMemTotal() on a missing file = %d, want 0", got)
	}
}

func TestLookupUser(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "passwd")
	body := "root:x:0:0:root:/root:/bin/bash\n# comment\npi:x:1000:1000:,,,:/home/pi:/bin/bash\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := passwdPath
	passwdPath = path
	defer func() { passwdPath = orig }()

	if got := lookupUser(1000); got != "pi" {
		t.Errorf("lookupUser(1000) = %q, want pi", got)
	}
	if got := lookupUser(0); got != "root" {
		t.Errorf("lookupUser(0) = %q, want root", got)
	}
	// An unknown uid falls back to the number rather than an empty column.
	if got := lookupUser(4242); got != "4242" {
		t.Errorf("lookupUser(4242) = %q, want 4242", got)
	}
}

func TestCmdlineString(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("ucmon\x00--once\x00"), "ucmon --once"},
		{[]byte("single\x00"), "single"},
		{[]byte{}, ""},
		{[]byte("\x00\x00"), ""}, // kernel thread
	}
	for _, c := range cases {
		if got := cmdlineString(c.in); got != c.want {
			t.Errorf("cmdlineString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestArgv0Base(t *testing.T) {
	cases := [][2]string{
		{"/usr/lib/systemd/systemd-journald --flush", "systemd-journald"},
		{"ucmon", "ucmon"},
		{"", ""},
	}
	for _, c := range cases {
		if got := argv0Base(c[0]); got != c[1] {
			t.Errorf("argv0Base(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// The kernel caps comm at 15 characters, so a long executable name arrives
// truncated and the command line is the only place the full one survives.
func TestSamplerRecoversTruncatedName(t *testing.T) {
	root := t.TempDir()
	pid := filepath.Join(root, "99")
	if err := os.MkdirAll(pid, 0o755); err != nil {
		t.Fatal(err)
	}
	// comm is exactly 15 characters, as the kernel would leave it.
	stat := "99 (systemd-journal) S 1 99 99 0 -1 0 0 0 0 0 10 20 0 0 20 0 1 0 500 1000 64 0 x"
	if err := os.WriteFile(filepath.Join(pid, "stat"), []byte(stat), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := []byte("/usr/lib/systemd/systemd-journald\x00")
	if err := os.WriteFile(filepath.Join(pid, "cmdline"), cmd, 0o644); err != nil {
		t.Fatal(err)
	}

	orig := procBase
	procBase = root
	defer func() { procBase = orig }()

	procs, err := NewProcSampler().List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(procs) != 1 {
		t.Fatalf("got %d rows, want 1", len(procs))
	}
	if procs[0].Name != "systemd-journald" {
		t.Errorf("Name = %q, want the full name recovered from cmdline", procs[0].Name)
	}
	if procs[0].PPID != 1 {
		t.Errorf("PPID = %d, want 1", procs[0].PPID)
	}
}

func TestRuneTrunc(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel"},
		{"héllo wörld", 4, "héll"}, // must not split multi-byte runes
		{"abc", 0, ""},
	}
	for _, c := range cases {
		if got := runeTrunc(c.in, c.n); got != c.want {
			t.Errorf("runeTrunc(%q,%d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestClampNice(t *testing.T) {
	cases := [][2]int{{-30, -20}, {-20, -20}, {0, 0}, {19, 19}, {25, 19}}
	for _, c := range cases {
		if got := ClampNice(c[0]); got != c[1] {
			t.Errorf("ClampNice(%d) = %d, want %d", c[0], got, c[1])
		}
	}
}
