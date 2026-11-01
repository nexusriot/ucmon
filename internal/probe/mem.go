package probe

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
)

type MemSnapshot struct {
	Total     uint64
	Used      uint64
	Free      uint64
	Available uint64
	Cached    uint64
	Buffers   uint64
	UsedPct   float64

	SwapTotal   uint64
	SwapUsed    uint64
	SwapUsedPct float64

	// OOMKills is the kernel's count of processes the OOM killer has killed
	// since boot (/proc/vmstat oom_kill, Linux 4.13+), cgroup limits
	// included. OOMKillsOK is false where the counter is missing.
	OOMKills   uint64
	OOMKillsOK bool

	TakenAt time.Time
}

func SampleMem() (MemSnapshot, error) {
	vm, err := mem.VirtualMemory()
	if err != nil {
		return MemSnapshot{}, err
	}

	snap := MemSnapshot{
		Total:     vm.Total,
		Used:      vm.Used,
		Free:      vm.Free,
		Available: vm.Available,
		Cached:    vm.Cached,
		Buffers:   vm.Buffers,
		UsedPct:   vm.UsedPercent,
		TakenAt:   time.Now(),
	}

	// Swap is optional; absence is not an error.
	if sm, err := mem.SwapMemory(); err == nil {
		snap.SwapTotal = sm.Total
		snap.SwapUsed = sm.Used
		snap.SwapUsedPct = sm.UsedPercent
	}

	snap.OOMKills, snap.OOMKillsOK = ReadOOMKills()
	return snap, nil
}

// vmstatPath is a var so tests can point it at a fixture.
var vmstatPath = "/proc/vmstat"

// ReadOOMKills returns the oom_kill counter from /proc/vmstat. A process the
// kernel kills for lack of memory otherwise just vanishes: nothing on screen
// says why the browser or the build disappeared.
func ReadOOMKills() (uint64, bool) {
	f, err := os.Open(vmstatPath)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, val, ok := strings.Cut(sc.Text(), " ")
		if !ok || name != "oom_kill" {
			continue
		}
		n, err := strconv.ParseUint(strings.TrimSpace(val), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// CheckOOMKills reports whether the OOM-kill counter is readable.
func CheckOOMKills() Capability {
	c := Capability{
		Name:    "OOM-kill counter",
		Source:  vmstatPath + " (oom_kill)",
		Affects: "the OOM-kill alert, Memory tab OOM line, ucmon_oom_kills_total",
	}
	if n, ok := ReadOOMKills(); ok {
		c.Status = CapPresent
		c.Detail = fmt.Sprintf("processes killed since boot: %d", n)
		return c
	}
	st, why := pathStatus(vmstatPath)
	c.Status = st
	switch st {
	case CapPresent:
		c.Status = CapAbsent
		c.Detail = "no oom_kill line — the kernel predates 4.13"
	default:
		c.Detail = why
	}
	return c
}
