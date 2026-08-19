package probe

import (
	"sort"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/shirou/gopsutil/v4/process"
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
}

// ProcSampler lists processes while reusing gopsutil Process handles across
// refreshes. gopsutil computes CPUPercent as the delta since the previous call
// on the same handle, so reuse makes the CPU%% column reflect usage since the
// last refresh (htop-style) instead of an average over the process lifetime.
// The first sample for a PID still reports the lifetime average.
type ProcSampler struct {
	mu    sync.Mutex
	procs map[int32]*process.Process
}

func NewProcSampler() *ProcSampler {
	return &ProcSampler{procs: map[int32]*process.Process{}}
}

func (s *ProcSampler) List() ([]ProcInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pids, err := process.Pids()
	if err != nil {
		return nil, err
	}

	seen := make(map[int32]bool, len(pids))
	out := make([]ProcInfo, 0, len(pids))
	for _, pid := range pids {
		seen[pid] = true
		p, ok := s.procs[pid]
		if !ok {
			p, err = process.NewProcess(pid)
			if err != nil {
				continue // exited between listing and open
			}
			s.procs[pid] = p
		}
		out = append(out, snapshotProc(p))
	}

	for pid := range s.procs {
		if !seen[pid] {
			delete(s.procs, pid)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].CPUPct > out[j].CPUPct
	})

	return out, nil
}

func snapshotProc(p *process.Process) ProcInfo {
	name, _ := p.Name()
	ppid, _ := p.Ppid()
	cpuPct, _ := p.CPUPercent()
	memPct, _ := p.MemoryPercent()
	memInfo, _ := p.MemoryInfo()
	status, _ := p.Status()
	user, _ := p.Username()
	cmdline, _ := p.Cmdline()

	var rss uint64
	if memInfo != nil {
		rss = memInfo.RSS
	}

	statusStr := ""
	if len(status) > 0 {
		statusStr = status[0]
	}

	return ProcInfo{
		PID:     p.Pid,
		PPID:    ppid,
		Name:    name,
		CPUPct:  cpuPct,
		MemPct:  memPct,
		MemRSS:  rss,
		Status:  statusStr,
		User:    user,
		CmdLine: runeTrunc(cmdline, 200),
	}
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
