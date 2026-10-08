package probe

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// blockBase is a var so tests can point the storage probe at a fake sysfs tree.
var blockBase = "/sys/block"

// StorageHealth describes the wear state of one storage device. Card death is
// the characteristic failure of a uConsole or Pi, and it is the one reading no
// other panel in ucmon could have hinted at: a card can be at 90% of its rated
// write endurance while every throughput and usage number looks perfect.
type StorageHealth struct {
	Device string // "mmcblk0", "nvme0n1", …
	Kind   string // "eMMC", "SD", "NVMe", "disk"
	Name   string // card/model name from the CID or model file
	Serial string
	SizeB  uint64

	// eMMC 5.0 device life time estimates, type A (SLC) and B (MLC). The
	// spec reports them in 10% bands: 1 means 0–10% of rated life used, 10
	// means 90–100%, 11 means the reserve blocks are exhausted.
	LifeA    int
	LifeB    int
	HasLife  bool
	PreEOL   int // 1 normal, 2 warning (80% of reserve used), 3 urgent
	HasEOL   bool
	ReadOnly bool // the controller flipped the card to read-only: it is done
}

// WearPct is the worst-case share of rated write endurance consumed, using the
// top of the reported band so the number never flatters the card.
func (s StorageHealth) WearPct() (float64, bool) {
	worst := s.LifeA
	if s.LifeB > worst {
		worst = s.LifeB
	}
	if worst < 1 || worst > 11 {
		return 0, false
	}
	if worst == 11 {
		return 100, true
	}
	return float64(worst) * 10, true
}

// EOLLabel renders the pre-EOL state, which is the reading that actually
// predicts failure: it tracks the reserve block pool, not writes.
func (s StorageHealth) EOLLabel() string {
	switch s.PreEOL {
	case 1:
		return "normal"
	case 2:
		return "warning"
	case 3:
		return "urgent"
	default:
		return ""
	}
}

// Severity grades a device: 0 fine, 1 worth watching, 2 replace it.
func (s StorageHealth) Severity() int {
	if s.ReadOnly || s.PreEOL == 3 {
		return 2
	}
	if pct, ok := s.WearPct(); ok && pct >= 90 {
		return 2
	}
	if s.PreEOL == 2 {
		return 1
	}
	if pct, ok := s.WearPct(); ok && pct >= 70 {
		return 1
	}
	return 0
}

// ReadStorageHealth walks /sys/block for devices that report wear data.
// Hosts whose storage exposes none (plain SD cards, most USB sticks, SATA)
// yield entries with only identity fields, and hosts with no /sys/block
// yield nothing — the same graceful-absence pattern as vcgencmd.
func ReadStorageHealth() []StorageHealth {
	entries, err := os.ReadDir(blockBase)
	if err != nil {
		return nil
	}

	var out []StorageHealth
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") ||
			strings.HasPrefix(name, "dm-") || strings.HasPrefix(name, "zram") {
			continue
		}
		if h, ok := readOneDevice(name); ok {
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

func readOneDevice(name string) (StorageHealth, bool) {
	dir := filepath.Join(blockBase, name)
	dev := filepath.Join(dir, "device")

	h := StorageHealth{Device: name, Kind: "disk"}

	if v, ok := sysReadInt(filepath.Join(dir, "size")); ok && v > 0 {
		h.SizeB = uint64(v) * 512 // sysfs size is always in 512-byte sectors
	}
	if v, ok := sysReadInt(filepath.Join(dir, "ro")); ok && v == 1 {
		h.ReadOnly = true
	}

	switch {
	case strings.HasPrefix(name, "mmcblk"):
		h.Kind = "SD"
		if t, ok := sysReadStr(filepath.Join(dev, "type")); ok && strings.EqualFold(t, "MMC") {
			h.Kind = "eMMC"
		}
		h.Name, _ = sysReadStr(filepath.Join(dev, "name"))
		h.Serial, _ = sysReadStr(filepath.Join(dev, "serial"))
		readMMCLifetime(dev, &h)
	case strings.HasPrefix(name, "nvme"):
		h.Kind = "NVMe"
		h.Name, _ = sysReadStr(filepath.Join(dev, "model"))
		h.Serial, _ = sysReadStr(filepath.Join(dev, "serial"))
	default:
		h.Name, _ = sysReadStr(filepath.Join(dev, "model"))
	}

	h.Name = strings.TrimSpace(h.Name)
	if h.Name == "" && h.SizeB == 0 {
		return StorageHealth{}, false
	}
	return h, true
}

// readMMCLifetime parses the eMMC health attributes. The kernel exposes them
// as the raw hex nibbles from the EXT_CSD register, e.g. "0x02 0x03".
func readMMCLifetime(dev string, h *StorageHealth) {
	if s, ok := sysReadStr(filepath.Join(dev, "life_time")); ok {
		fields := strings.Fields(s)
		if len(fields) >= 1 {
			if v, ok := parseHexByte(fields[0]); ok {
				h.LifeA, h.HasLife = v, true
			}
		}
		if len(fields) >= 2 {
			if v, ok := parseHexByte(fields[1]); ok {
				h.LifeB, h.HasLife = v, true
			}
		}
	}
	if s, ok := sysReadStr(filepath.Join(dev, "pre_eol_info")); ok {
		if v, ok := parseHexByte(s); ok && v > 0 {
			h.PreEOL, h.HasEOL = v, true
		}
	}
}

// parseHexByte accepts "0x02" or a bare "2".
func parseHexByte(s string) (int, bool) {
	s = strings.TrimSpace(s)
	base := 10
	if lower := strings.ToLower(s); strings.HasPrefix(lower, "0x") {
		s, base = lower[2:], 16
	}
	v, err := strconv.ParseInt(s, base, 32)
	if err != nil {
		return 0, false
	}
	return int(v), true
}
