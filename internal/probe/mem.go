package probe

import (
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
	SwapFree    uint64
	SwapUsedPct float64

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
		snap.SwapFree = sm.Free
		snap.SwapUsedPct = sm.UsedPercent
	}

	return snap, nil
}
