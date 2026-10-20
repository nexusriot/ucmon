package probe

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/process"
	"github.com/tklauser/go-sysconf"
	"golang.org/x/sys/unix"
)

type ProcInfo struct {
	PID     int32
	PPID    int32
	Name    string
	CPUPct  float64
	MemPct  float32
	MemRSS  uint64
	Status  string
	User    string
	CmdLine string

	// Per-process block-I/O rates since the previous refresh. Zero when
	// /proc/<pid>/io is unreadable, which it is for other users' processes
	// without CAP_SYS_PTRACE.
	IOReadBps  float64
	IOWriteBps float64
	IOKnown    bool
}

// procBase is a var so tests can point the sampler at a fake /proc tree.
var procBase = "/proc"

// clockTicks is USER_HZ: the unit of the utime/stime/starttime columns in
// /proc/<pid>/stat. It is 100 on every Linux/arch combination ucmon ships
// for, but sysconf settles it rather than assuming.
var clockTicks = func() float64 {
	if n, err := sysconf.Sysconf(sysconf.SC_CLK_TCK); err == nil && n > 0 {
		return float64(n)
	}
	return 100
}()

var pageSize = uint64(os.Getpagesize())

// procSample is the previous refresh's raw counters for one PID, kept so the
// next refresh can report rates over the interval rather than over the
// process lifetime.
type procSample struct {
	cpuTicks  uint64 // utime + stime
	startTime uint64 // field 22: identifies the process across PID reuse
	ioRead    uint64
	ioWrite   uint64
	at        time.Time
}

// ProcSampler walks /proc directly instead of going through gopsutil, and
// keeps the previous refresh's counters so CPU% and per-process I/O are rates
// over the interval (htop-style) rather than lifetime averages.
//
// Going direct matters on the boards ucmon targets. gopsutil's accessors each
// re-read and re-parse the file they need, so one refresh read /proc/<pid>/stat
// twice (Ppid + CPUPercent), /proc/<pid>/status twice (Status + Username),
// scanned /etc/passwd once per process (pure-Go user.LookupId), and parsed
// /proc/meminfo once per process (MemoryPercent). Measured over 533 processes
// that was ~90 ms of work for ~7 ms of actual file reads.
type ProcSampler struct {
	mu    sync.Mutex
	prev  map[int32]procSample
	users map[uint32]string // uid -> name, resolved once per uid per process
}

func NewProcSampler() *ProcSampler {
	return &ProcSampler{
		prev:  map[int32]procSample{},
		users: map[uint32]string{},
	}
}

func (s *ProcSampler) List() ([]ProcInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(procBase)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	memTotal := readMemTotal()

	seen := make(map[int32]bool, len(entries))
	out := make([]ProcInfo, 0, len(entries))

	for _, e := range entries {
		pid64, err := strconv.ParseInt(e.Name(), 10, 32)
		if err != nil || pid64 <= 0 {
			continue // not a pid directory
		}
		pid := int32(pid64)

		info, cur, ok := s.readProc(pid, memTotal, now)
		if !ok {
			continue // exited between the listing and the read
		}
		seen[pid] = true
		s.prev[pid] = cur
		out = append(out, info)
	}

	for pid := range s.prev {
		if !seen[pid] {
			delete(s.prev, pid)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CPUPct > out[j].CPUPct
	})

	return out, nil
}

// readProc builds one row from /proc/<pid>. It reads stat and cmdline, stats
// the directory for the owner uid, and reads io when permitted — four cheap
// operations against the eight parsing round trips the gopsutil path took.
func (s *ProcSampler) readProc(pid int32, memTotal uint64, now time.Time) (ProcInfo, procSample, bool) {
	dir := filepath.Join(procBase, strconv.Itoa(int(pid)))

	statBytes, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		return ProcInfo{}, procSample{}, false
	}
	st, ok := parseProcStat(statBytes)
	if !ok {
		return ProcInfo{}, procSample{}, false
	}

	info := ProcInfo{
		PID:    pid,
		PPID:   st.ppid,
		Name:   st.comm,
		Status: st.state,
		MemRSS: st.rssPages * pageSize,
	}
	if memTotal > 0 {
		info.MemPct = float32(float64(info.MemRSS) / float64(memTotal) * 100)
	}

	if b, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		info.CmdLine = runeTrunc(cmdlineString(b), 200)
		// comm is capped at 15 characters by the kernel; the executable's
		// own name from argv[0] is the better label when it was cut.
		if utf8.RuneCountInString(info.Name) >= 15 {
			if full := argv0Base(info.CmdLine); full != "" {
				info.Name = full
			}
		}
	}

	info.User = s.userName(dir)

	cur := procSample{cpuTicks: st.utime + st.stime, startTime: st.startTime, at: now}
	if ioRead, ioWrite, ok := readProcIO(dir); ok {
		cur.ioRead, cur.ioWrite = ioRead, ioWrite
		info.IOKnown = true
	}

	// A PID the kernel recycled is a different process: its counters restart,
	// so a delta against the old ones would report a wild rate. startTime
	// settles it, and it costs nothing — it is already in the stat we read.
	if prev, ok := s.prev[pid]; ok && prev.startTime == st.startTime {
		if dt := now.Sub(prev.at).Seconds(); dt > 0 {
			info.CPUPct = float64(cur.cpuTicks-prev.cpuTicks) / clockTicks / dt * 100
			if info.CPUPct < 0 {
				info.CPUPct = 0
			}
			if info.IOKnown {
				info.IOReadBps = CounterRate(cur.ioRead, prev.ioRead, dt)
				info.IOWriteBps = CounterRate(cur.ioWrite, prev.ioWrite, dt)
			}
		}
	}

	return info, cur, true
}

// userName resolves the owner of a /proc/<pid> directory, caching by uid. The
// directory's own ownership gives the uid in one stat call, where reading
// /proc/<pid>/status means parsing fifty lines for one of them.
func (s *ProcSampler) userName(dir string) string {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return ""
	}
	uid := st.Uid
	if name, ok := s.users[uid]; ok {
		return name
	}
	name := lookupUser(uid)
	s.users[uid] = name
	return name
}

// passwdPath is a var so tests can point the uid lookup at a fake file.
var passwdPath = "/etc/passwd"

// lookupUser maps a uid to a login name via /etc/passwd, falling back to the
// numeric uid. os/user would do the same scan, but once per call rather than
// once per uid, which is a full file scan per process in a pure-Go build.
func lookupUser(uid uint32) string {
	want := strconv.FormatUint(uint64(uid), 10)
	if b, err := os.ReadFile(passwdPath); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Split(line, ":")
			if len(fields) >= 3 && fields[2] == want {
				return fields[0]
			}
		}
	}
	return want
}

// procStat holds the /proc/<pid>/stat columns ucmon uses.
type procStat struct {
	comm      string
	state     string
	ppid      int32
	utime     uint64
	stime     uint64
	startTime uint64
	rssPages  uint64
}

// parseProcStat reads the columns ucmon needs out of /proc/<pid>/stat. The
// comm field is wrapped in parentheses and may itself contain spaces and
// parentheses, so the split starts after the *last* ')' rather than at the
// second field.
func parseProcStat(b []byte) (procStat, bool) {
	var st procStat

	open := bytes.IndexByte(b, '(')
	close := bytes.LastIndexByte(b, ')')
	if open < 0 || close < open {
		return st, false
	}
	st.comm = string(b[open+1 : close])

	// Columns are 1-indexed in proc(5) and the first one after ')' is state
	// (column 3), so column N lives at index N-3.
	fields := strings.Fields(string(b[close+1:]))
	const (
		idxState     = 0  // column 3
		idxPPID      = 1  // column 4
		idxUTime     = 11 // column 14
		idxSTime     = 12 // column 15
		idxStartTime = 19 // column 22
		idxRSS       = 21 // column 24, in pages
	)
	if len(fields) <= idxRSS {
		return st, false
	}

	st.state = fields[idxState]
	if v, err := strconv.ParseInt(fields[idxPPID], 10, 32); err == nil {
		st.ppid = int32(v)
	}
	st.utime, _ = strconv.ParseUint(fields[idxUTime], 10, 64)
	st.stime, _ = strconv.ParseUint(fields[idxSTime], 10, 64)
	st.startTime, _ = strconv.ParseUint(fields[idxStartTime], 10, 64)
	// rss is signed in proc(5) and reads 0 for kernel threads.
	if v, err := strconv.ParseInt(fields[idxRSS], 10, 64); err == nil && v > 0 {
		st.rssPages = uint64(v)
	}
	return st, true
}

// readProcIO returns the cumulative read_bytes / write_bytes counters, which
// are the ones that reach the block layer (read/write count what the process
// asked for, cache hits included). Unreadable for other users' processes
// without CAP_SYS_PTRACE, which is reported rather than faked as zero.
func readProcIO(dir string) (read, write uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(dir, "io"))
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		name, val, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		v, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		if err != nil {
			continue
		}
		switch name {
		case "read_bytes":
			read, ok = v, true
		case "write_bytes":
			write, ok = v, true
		}
	}
	return read, write, ok
}

// meminfoPath is a var so tests can point readMemTotal at a fake file.
var meminfoPath = "/proc/meminfo"

// readMemTotal reads MemTotal once per refresh. gopsutil's MemoryPercent
// re-read and re-parsed this file for every process in the list.
func readMemTotal() uint64 {
	b, err := os.ReadFile(meminfoPath)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// cmdlineString joins the NUL-separated argv of /proc/<pid>/cmdline. Kernel
// threads have an empty one.
func cmdlineString(b []byte) string {
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return ""
	}
	return string(bytes.ReplaceAll(b, []byte{0}, []byte{' '}))
}

// argv0Base is the basename of a command line's first word, used to recover a
// name the kernel truncated to 15 characters in comm.
func argv0Base(cmdline string) string {
	first, _, _ := strings.Cut(cmdline, " ")
	if first == "" {
		return ""
	}
	return filepath.Base(first)
}

// runeTrunc cuts s to at most n runes without splitting a UTF-8 sequence.
func runeTrunc(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// ProcDetail holds the extended, on-demand readings for one process shown in
// the detail overlay. Fields that need permissions the viewer lacks degrade
// to their zero value (FDs to -1).
type ProcDetail struct {
	PID        int32
	Name       string
	User       string
	Status     string
	CmdLine    string
	Exe        string
	Cwd        string
	Nice       int32
	Threads    int32
	FDs        int32 // -1 when unreadable (permissions)
	MemRSS     uint64
	MemVMS     uint64
	MemSwap    uint64
	CreateTime int64 // unix ms, 0 when unknown
	IORead     uint64
	IOWrite    uint64
	IOOK       bool
	Children   int
	Conns      []ConnInfo
}

// GetProcDetail reads one process in depth for the detail overlay. This path
// stays on gopsutil: it runs for a single PID on the proc tick, so the
// per-call parsing overhead that makes it wrong for the whole-list walk is
// irrelevant here, and it carries exe/cwd/fd/children for free.
func GetProcDetail(pid int32) (ProcDetail, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcDetail{}, err
	}

	d := ProcDetail{PID: pid, FDs: -1}
	d.Name, _ = p.Name()
	d.User, _ = p.Username()
	if st, _ := p.Status(); len(st) > 0 {
		d.Status = st[0]
	}
	d.CmdLine, _ = p.Cmdline()
	d.Exe, _ = p.Exe()
	d.Cwd, _ = p.Cwd()
	d.Nice = int32(getNice(pid))
	d.Threads, _ = p.NumThreads()
	if n, err := p.NumFDs(); err == nil {
		d.FDs = n
	}
	if mi, _ := p.MemoryInfo(); mi != nil {
		d.MemRSS, d.MemVMS, d.MemSwap = mi.RSS, mi.VMS, mi.Swap
	}
	d.CreateTime, _ = p.CreateTime()
	if io, err := p.IOCounters(); err == nil && io != nil {
		d.IORead, d.IOWrite, d.IOOK = io.ReadBytes, io.WriteBytes, true
	}
	if kids, err := p.Children(); err == nil {
		d.Children = len(kids)
	}
	d.Conns = ConnectionsForPID(pid)
	return d, nil
}

// KillProcess sends SIGTERM (hard=false) or SIGKILL (hard=true) to pid.
func KillProcess(pid int32, hard bool) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	if hard {
		return p.Kill()
	}
	return p.Terminate()
}

// SuspendProcess sends SIGSTOP to pid.
func SuspendProcess(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Suspend()
}

// ResumeProcess sends SIGCONT to pid.
func ResumeProcess(pid int32) error {
	p, err := process.NewProcess(pid)
	if err != nil {
		return err
	}
	return p.Resume()
}

// getNice returns pid's real niceness (-20..19). getpriority(2) returns
// 20-nice at the syscall boundary so a valid low priority can never be
// mistaken for the -1 error value; glibc undoes that shift but
// golang.org/x/sys/unix does not, and gopsutil's Process.Nice() forwards the
// raw number (its source carries a "FIXME: is this true?" on that line). Left
// unconverted, a normal process reads as nice 20, and renicing by ±1 from
// there lands on 19 — the lowest priority — in a single keypress.
func getNice(pid int32) int {
	raw, err := unix.Getpriority(unix.PRIO_PROCESS, int(pid))
	if err != nil {
		return 0
	}
	return 20 - raw
}

// ClampNice bounds a niceness value to the kernel's -20..19 range.
func ClampNice(n int) int {
	if n < -20 {
		return -20
	}
	if n > 19 {
		return 19
	}
	return n
}

// SetNice renices pid. Lowering niceness below 0 needs CAP_SYS_NICE / root;
// the raw EPERM/EACCES error is returned for the UI to surface.
func SetNice(pid int32, nice int) error {
	return syscall.Setpriority(syscall.PRIO_PROCESS, int(pid), ClampNice(nice))
}

// CheckProcIO reports whether per-process I/O counters are readable beyond
// this user's own processes. /proc/<pid>/io is 0400 and owned by the process
// owner, so without CAP_SYS_PTRACE (or root) the Processes tab can show disk
// I/O for the user's own processes and nothing else — and a system that is
// visibly writing shows a column of dashes.
func CheckProcIO() Capability {
	c := Capability{
		Name:    "per-process disk I/O",
		Source:  filepath.Join(procBase, "<pid>", "io"),
		Affects: "the Processes tab I/O columns and the process detail view",
		Fix: "run ucmon as root, or grant the binary the capability:\n" +
			"sudo setcap cap_sys_ptrace+ep $(command -v ucmon)",
	}

	selfDir := filepath.Join(procBase, strconv.Itoa(os.Getpid()))
	if _, _, ok := readProcIO(selfDir); !ok {
		st, why := pathStatus(filepath.Join(selfDir, "io"))
		switch st {
		case CapDenied:
			c.Status = CapDenied
			c.Detail = why + " — even this process's own counters are refused"
		default:
			c.Status = CapAbsent
			c.Detail = why + " — kernel built without CONFIG_TASK_IO_ACCOUNTING"
			c.Fix = "" // no capability brings back a counter the kernel never keeps
		}
		return c
	}

	// pid 1 belongs to root on any booted system, so it is the cheapest
	// probe for "can we see other users' processes".
	other := filepath.Join(procBase, "1")
	if _, _, ok := readProcIO(other); !ok {
		c.Status = CapDenied
		c.Detail = "own processes only; " + filepath.Join(other, "io") + " is unreadable without CAP_SYS_PTRACE"
		return c
	}

	c.Status = CapPresent
	c.Detail = "readable for every process"
	c.Fix = ""
	return c
}
