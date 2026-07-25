package probe

import (
	"sort"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

type DiskInfo struct {
	Device     string
	MountPoint string
	FSType     string
	Total      uint64
	Used       uint64
	Free       uint64
	UsedPct    float64
}

type DiskIOInfo struct {
	Device     string
	ReadBytes  uint64
	WriteBytes uint64
	ReadBps    float64
	WriteBps   float64
}

type DiskSampler struct {
	mu     sync.Mutex
	lastIO map[string]disk.IOCountersStat
	lastAt time.Time
}

func NewDiskSampler() *DiskSampler {
	return &DiskSampler{
		lastIO: map[string]disk.IOCountersStat{},
	}
}

func (s *DiskSampler) Sample() ([]DiskInfo, []DiskIOInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, nil, err
	}

	var disks []DiskInfo
	seen := map[string]bool{}
	for _, p := range partitions {
		if seen[p.Mountpoint] {
			continue
		}
		seen[p.Mountpoint] = true

		usage, err := disk.Usage(p.Mountpoint)
		if err != nil || usage.Total == 0 {
			continue
		}

		disks = append(disks, DiskInfo{
			Device:     p.Device,
			MountPoint: p.Mountpoint,
			FSType:     p.Fstype,
			Total:      usage.Total,
			Used:       usage.Used,
			Free:       usage.Free,
			UsedPct:    usage.UsedPercent,
		})
	}

	// IO counters
	ioCounters, err := disk.IOCounters()
	if err != nil {
		return disks, nil, nil
	}

	// Rates are computed over the real elapsed time since the previous sample
	// (the sampler runs on the slow interval, not once per second).
	dt := now.Sub(s.lastAt).Seconds()
	if s.lastAt.IsZero() || dt <= 0 {
		dt = 1
	}

	ios := ioRates(s.lastIO, ioCounters, dt)

	s.lastIO = ioCounters
	s.lastAt = now
	return disks, ios, nil
}

// ioRates computes per-device read/write rates from two counter snapshots
// taken dt seconds apart. Devices without a previous snapshot report 0.
func ioRates(prev, cur map[string]disk.IOCountersStat, dt float64) []DiskIOInfo {
	if dt <= 0 {
		dt = 1
	}
	ios := make([]DiskIOInfo, 0, len(cur))
	for name, c := range cur {
		di := DiskIOInfo{
			Device:     name,
			ReadBytes:  c.ReadBytes,
			WriteBytes: c.WriteBytes,
		}
		if p, ok := prev[name]; ok {
			di.ReadBps = float64(c.ReadBytes-p.ReadBytes) / dt
			di.WriteBps = float64(c.WriteBytes-p.WriteBytes) / dt
		}
		ios = append(ios, di)
	}
	sort.Slice(ios, func(i, j int) bool { return ios[i].Device < ios[j].Device })
	return ios
}
