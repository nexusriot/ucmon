package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/sensors"
)

type CPUSnapshot struct {
	PerCorePercent []float64
	TotalPercent   float64
	Breakdown      CPUBreakdown // where the busy time went over the interval
	Temperatures   []TempReading
	Freqs          []PolicyFreq // cpufreq policies; empty without cpufreq support
	TakenAt        time.Time
}

// CPUBreakdown splits the interval's CPU time into the categories that mean
// different things on a Pi. A total of "80% busy" is one number for two
// opposite situations: user code saturating the cores, or every core parked
// in iowait behind a slow SD card. Only the split tells them apart.
type CPUBreakdown struct {
	Valid   bool // false until two samples exist
	User    float64
	System  float64
	Nice    float64
	Iowait  float64
	IRQ     float64 // hardirq + softirq
	Steal   float64
	Idle    float64
	Busiest string // label of the largest non-idle share
}

type TempReading struct {
	Label string
	Temp  float64
}

// lastCPUTimes holds the previous cpu.Times reading so the breakdown is a
// share of the interval rather than of uptime. Guarded because SampleCPU runs
// on a Bubble Tea command goroutine.
var (
	cpuTimesMu   sync.Mutex
	lastCPUTimes *cpu.TimesStat
)

func SampleCPU() (CPUSnapshot, error) {
	perCore, err := cpu.Percent(0, true)
	if err != nil {
		return CPUSnapshot{}, err
	}

	total, err := cpu.Percent(0, false)
	if err != nil {
		return CPUSnapshot{}, err
	}

	totalPct := 0.0
	if len(total) > 0 {
		totalPct = total[0]
	}

	temps := readTemperatures()

	return CPUSnapshot{
		PerCorePercent: perCore,
		TotalPercent:   totalPct,
		Breakdown:      sampleBreakdown(),
		Temperatures:   temps,
		Freqs:          ReadCPUFreq(),
		TakenAt:        time.Now(),
	}, nil
}

// sampleBreakdown diffs /proc/stat's aggregate CPU line against the previous
// reading. The first call has nothing to diff and reports Valid=false.
func sampleBreakdown() CPUBreakdown {
	times, err := cpu.Times(false)
	if err != nil || len(times) == 0 {
		return CPUBreakdown{}
	}
	cur := times[0]

	cpuTimesMu.Lock()
	prev := lastCPUTimes
	lastCPUTimes = &cur
	cpuTimesMu.Unlock()

	if prev == nil {
		return CPUBreakdown{}
	}
	return diffCPUTimes(*prev, cur)
}

// diffCPUTimes converts two cumulative cpu.TimesStat readings into the
// interval's percentage split.
func diffCPUTimes(prev, cur cpu.TimesStat) CPUBreakdown {
	d := func(a, b float64) float64 {
		if v := a - b; v > 0 {
			return v
		}
		return 0 // a counter reset or a CPU coming online mid-interval
	}

	user := d(cur.User, prev.User)
	system := d(cur.System, prev.System)
	nice := d(cur.Nice, prev.Nice)
	iowait := d(cur.Iowait, prev.Iowait)
	irq := d(cur.Irq, prev.Irq) + d(cur.Softirq, prev.Softirq)
	steal := d(cur.Steal, prev.Steal)
	idle := d(cur.Idle, prev.Idle)

	total := user + system + nice + iowait + irq + steal + idle
	if total <= 0 {
		return CPUBreakdown{}
	}

	b := CPUBreakdown{
		Valid:  true,
		User:   user / total * 100,
		System: system / total * 100,
		Nice:   nice / total * 100,
		Iowait: iowait / total * 100,
		IRQ:    irq / total * 100,
		Steal:  steal / total * 100,
		Idle:   idle / total * 100,
	}

	for _, c := range []struct {
		label string
		pct   float64
	}{
		{"user", b.User}, {"system", b.System}, {"nice", b.Nice},
		{"iowait", b.Iowait}, {"irq", b.IRQ}, {"steal", b.Steal},
	} {
		if c.pct > 0.5 && (b.Busiest == "" || c.pct > breakdownShare(b, b.Busiest)) {
			b.Busiest = c.label
		}
	}
	return b
}

// breakdownShare looks a category back up by label.
func breakdownShare(b CPUBreakdown, label string) float64 {
	switch label {
	case "user":
		return b.User
	case "system":
		return b.System
	case "nice":
		return b.Nice
	case "iowait":
		return b.Iowait
	case "irq":
		return b.IRQ
	case "steal":
		return b.Steal
	}
	return 0
}

func readTemperatures() []TempReading {
	// Try gopsutil sensors package first
	stats, err := sensors.SensorsTemperatures()
	if err == nil && len(stats) > 0 {
		var out []TempReading
		for _, s := range stats {
			if s.Temperature > 0 && s.Temperature < 150 {
				out = append(out, TempReading{
					Label: s.SensorKey,
					Temp:  s.Temperature,
				})
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Fallback: read thermal_zone sysfs (common on RPi/uConsole)
	return readThermalZones()
}

// thermalBase is a var so tests can point it at a fake sysfs tree.
var thermalBase = "/sys/class/thermal"

func readThermalZones() []TempReading {
	entries, err := os.ReadDir(thermalBase)
	if err != nil {
		return nil
	}

	var out []TempReading
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		zpath := filepath.Join(thermalBase, e.Name())

		label := e.Name()
		if b, err := os.ReadFile(filepath.Join(zpath, "type")); err == nil {
			label = strings.TrimSpace(string(b))
		}

		b, err := os.ReadFile(filepath.Join(zpath, "temp"))
		if err != nil {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			continue
		}
		// millidegrees -> degrees
		if val > 1000 {
			val /= 1000.0
		}
		if val > 0 && val < 150 {
			out = append(out, TempReading{
				Label: label,
				Temp:  val,
			})
		}
	}
	return out
}

// Temperature color thresholds (°C). Overridable from config via
// SetTempThresholds; defaults match the original hard-coded values.
var (
	tempWarn float64 = 50 // yellow
	tempHot  float64 = 65 // orange
	tempCrit float64 = 80 // red
)

// SetTempThresholds overrides the warn/hot/crit boundaries used by TempColor.
func SetTempThresholds(warn, hot, crit float64) {
	if warn > 0 {
		tempWarn = warn
	}
	if hot > 0 {
		tempHot = hot
	}
	if crit > 0 {
		tempCrit = crit
	}
}

// TripPoints are the thermal limits the kernel itself declares for a zone,
// in °C. "passive" is where the firmware begins throttling; "critical" is
// where it shuts down.
type TripPoints struct {
	Zone     string
	Passive  float64 // 0 when the zone declares none
	Critical float64 // 0 when the zone declares none
	Hot      float64 // some zones expose a "hot" trip between the two
}

// Throttle returns the temperature the board starts throttling at — the
// passive trip if there is one, else "hot".
func (t TripPoints) Throttle() float64 {
	if t.Passive > 0 {
		return t.Passive
	}
	return t.Hot
}

// ReadTripPoints returns the trip points of the first thermal zone that
// declares any, reading the same directory readThermalZones already walks.
//
// The hard-coded 50/65/80 defaults happen to suit a Raspberry Pi, whose
// firmware throttles at 80 °C. They are wrong on anything else: this
// developer machine declares a 109 °C critical trip, so its CPU shows red
// nearly 30 °C before the hardware considers it a problem. Asking the kernel
// calibrates the colours to the board ucmon is actually running on.
func ReadTripPoints() (TripPoints, bool) {
	entries, err := os.ReadDir(thermalBase)
	if err != nil {
		return TripPoints{}, false
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "thermal_zone") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return zoneIndex(names[i]) < zoneIndex(names[j]) })

	for _, name := range names {
		zpath := filepath.Join(thermalBase, name)
		tp := TripPoints{Zone: name}
		if label, err := os.ReadFile(filepath.Join(zpath, "type")); err == nil {
			tp.Zone = strings.TrimSpace(string(label))
		}

		for i := 0; ; i++ {
			kind, err := os.ReadFile(filepath.Join(zpath, fmt.Sprintf("trip_point_%d_type", i)))
			if err != nil {
				break
			}
			raw, err := os.ReadFile(filepath.Join(zpath, fmt.Sprintf("trip_point_%d_temp", i)))
			if err != nil {
				continue
			}
			v, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
			if err != nil {
				continue
			}
			if v > 1000 {
				v /= 1000 // millidegrees
			}
			if v <= 0 || v > 200 {
				continue // a disabled trip reads as 0 or a sentinel
			}
			switch strings.TrimSpace(string(kind)) {
			case "passive":
				if tp.Passive == 0 || v < tp.Passive {
					tp.Passive = v // the lowest passive trip is the first to bite
				}
			case "critical":
				tp.Critical = v
			case "hot":
				tp.Hot = v
			}
		}

		if tp.Critical > 0 || tp.Passive > 0 || tp.Hot > 0 {
			return tp, true
		}
	}
	return TripPoints{}, false
}

// zoneIndex extracts the number from "thermal_zone12" so zones sort
// numerically rather than lexicographically (which puts zone10 before zone2).
func zoneIndex(name string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(name, "thermal_zone"))
	if err != nil {
		return 1 << 30
	}
	return n
}

// TripThresholds derives warn/hot/crit colour boundaries from a zone's own
// trip points: red at the shutdown trip, orange where throttling begins, and
// yellow a proportional step below that.
func TripThresholds(tp TripPoints) (warn, hot, crit float64, ok bool) {
	crit = tp.Critical
	hot = tp.Throttle()

	switch {
	case crit > 0 && hot > 0:
	case crit > 0:
		hot = crit * 0.8
	case hot > 0:
		crit = hot * 1.15
	default:
		return 0, 0, 0, false
	}

	if hot >= crit {
		hot = crit * 0.8
	}
	warn = hot * 0.75
	return warn, hot, crit, true
}

func TempColor(t float64) string {
	switch {
	case t >= tempCrit:
		return "196" // red
	case t >= tempHot:
		return "214" // orange
	case t >= tempWarn:
		return "226" // yellow
	default:
		return "42" // green
	}
}

func FormatTemp(t float64) string {
	return fmt.Sprintf("%.1f°C", t)
}
