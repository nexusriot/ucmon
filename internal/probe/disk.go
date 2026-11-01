package probe

import (
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
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

	// ReadOnly is the live mount state ("ro" in the mount options).
	// RemountedRO says it was not meant to be: the filesystem is read-only
	// now, but /etc/fstab declares it read-write or this sampler saw it
	// mounted read-write earlier. That is the kernel's errors=remount-ro
	// reaction to I/O errors — on an SD-booted board, the usual first sign
	// the card is dying, and one the device-level wear counters never show.
	ReadOnly    bool
	RemountedRO bool
}

type DiskIOInfo struct {
	Device   string
	ReadBps  float64
	WriteBps float64
}

type DiskSampler struct {
	mu     sync.Mutex
	lastIO map[string]disk.IOCountersStat
	lastAt time.Time
	// sawRW maps a mount point to the device last seen mounted read-write
	// there; the device is kept so a different filesystem mounted read-only
	// at the same path is not mistaken for a remount.
	sawRW map[string]string
}

func NewDiskSampler() *DiskSampler {
	return &DiskSampler{
		lastIO: map[string]disk.IOCountersStat{},
		sawRW:  map[string]string{},
	}
}

// fstabPath and mountsPath are vars so tests can point them at fixtures.
var (
	fstabPath  = "/etc/fstab"
	mountsPath = "/proc/self/mounts"
)

// liveReadOnly maps each mount point to whether it is read-only right now.
// It reads /proc/self/mounts rather than trusting gopsutil's Opts, which come
// from mountinfo's per-mount flags: errors=remount-ro flips only the
// superblock read-only, so those flags keep saying "rw" on a filesystem the
// kernel has already locked. /proc/mounts reports the combined state. Where
// a path is mounted over, the last (visible) mount wins.
func liveReadOnly() (map[string]bool, bool) {
	b, err := os.ReadFile(mountsPath)
	if err != nil {
		return nil, false
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		out[unescapeMountPath(fields[1])] = slices.Contains(strings.Split(fields[3], ","), "ro")
	}
	return out, true
}

// readOnlyByDesign lists filesystems that cannot be mounted read-write, so a
// read-only mount of one is never a remount.
var readOnlyByDesign = map[string]bool{"squashfs": true, "iso9660": true, "erofs": true, "udf": true, "cramfs": true}

// ReadOnlyByDesign reports whether fstype is an image format that is always
// read-only and, being packed to size, always reads 100% full.
func ReadOnlyByDesign(fstype string) bool { return readOnlyByDesign[fstype] }

// fstabRW returns the mount points /etc/fstab declares, mapped to whether
// they are meant to be read-write. A missing or unreadable fstab yields an
// empty map: the sampler's own history is then the only evidence.
func fstabRW() map[string]bool {
	out := map[string]bool{}
	b, err := os.ReadFile(fstabPath)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		mp, fstype := unescapeMountPath(fields[1]), fields[2]
		if fstype == "swap" || readOnlyByDesign[fstype] {
			continue
		}
		out[mp] = !slices.Contains(strings.Split(fields[3], ","), "ro")
	}
	return out
}

// unescapeMountPath decodes the octal escapes fstab and /proc/mounts use
// (\040 for a space).
func unescapeMountPath(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// markRemounts fills ReadOnly and RemountedRO, and records which mounts
// were seen read-write. Callers hold s.mu.
func (s *DiskSampler) markRemounts(d *DiskInfo, ro bool, fstab map[string]bool) {
	d.ReadOnly = ro
	if !d.ReadOnly {
		s.sawRW[d.MountPoint] = d.Device
		return
	}
	if readOnlyByDesign[d.FSType] {
		return
	}
	if dev, ok := s.sawRW[d.MountPoint]; ok && dev == d.Device {
		d.RemountedRO = true
		return
	}
	d.RemountedRO = fstab[d.MountPoint]
}

func (s *DiskSampler) Sample() ([]DiskInfo, []DiskIOInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, nil, err
	}

	fstab := fstabRW()
	live, liveOK := liveReadOnly()
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

		d := DiskInfo{
			Device:     p.Device,
			MountPoint: p.Mountpoint,
			FSType:     p.Fstype,
			Total:      usage.Total,
			Used:       usage.Used,
			Free:       usage.Free,
			UsedPct:    usage.UsedPercent,
		}
		ro, ok := live[p.Mountpoint]
		if !liveOK || !ok {
			ro = slices.Contains(p.Opts, "ro") // per-mount flag only; see liveReadOnly
		}
		s.markRemounts(&d, ro, fstab)
		disks = append(disks, d)
	}

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
		di := DiskIOInfo{Device: name}
		if p, ok := prev[name]; ok {
			di.ReadBps = CounterRate(c.ReadBytes, p.ReadBytes, dt)
			di.WriteBps = CounterRate(c.WriteBytes, p.WriteBytes, dt)
		}
		ios = append(ios, di)
	}
	sort.Slice(ios, func(i, j int) bool { return ios[i].Device < ios[j].Device })
	return ios
}
