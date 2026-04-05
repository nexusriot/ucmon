package probe

import (
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
	lastIO map[string]disk.IOCountersStat
}

func NewDiskSampler() *DiskSampler {
	return &DiskSampler{
		lastIO: map[string]disk.IOCountersStat{},
	}
}

func (s *DiskSampler) Sample(dt float64) ([]DiskInfo, []DiskIOInfo, error) {
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

	if dt <= 0 {
		dt = 1
	}

	var ios []DiskIOInfo
	for name, cur := range ioCounters {
		di := DiskIOInfo{
			Device:     name,
			ReadBytes:  cur.ReadBytes,
			WriteBytes: cur.WriteBytes,
		}
		if prev, ok := s.lastIO[name]; ok {
			di.ReadBps = float64(cur.ReadBytes-prev.ReadBytes) / dt
			di.WriteBps = float64(cur.WriteBytes-prev.WriteBytes) / dt
		}
		ios = append(ios, di)
	}

	s.lastIO = ioCounters
	return disks, ios, nil
}
