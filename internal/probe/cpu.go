package probe

import (
	"errors"
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

// CPUSampler keeps the previous /proc/stat reading so utilisation and the
// time breakdown are shares of the interval rather than of uptime.
//
// The state is per-sampler rather than package-level on purpose. gopsutil's
// cpu.Percent(0, …) keeps its baseline in a package global, so two callers in
// one process silently measure each other's intervals: with metrics_listen
// set, every scrape re-primed the baseline the TUI was about to diff against,
// and the next frame showed a 600 ms window — or 0% outright, when the two
// landed close enough together — instead of the second it claimed. The net,
// disk and process samplers already own their own state; this one now does
// too. Reading /proc/stat directly also costs two reads per tick rather than
// the three cpu.Percent + cpu.Times needed.
type CPUSampler struct {
	mu      sync.Mutex
	lastAll *cpu.TimesStat
	lastPer []cpu.TimesStat
}

// NewCPUSampler returns a sampler with no baseline; its first Sample reports
// 0% and an invalid breakdown, and every one after that is a real interval.
func NewCPUSampler() *CPUSampler { return &CPUSampler{} }

// Sample reads the CPU counters, temperatures and cpufreq policies once.
func (s *CPUSampler) Sample() (CPUSnapshot, error) {
	all, err := cpu.Times(false)
	if err != nil {
		return CPUSnapshot{}, err
	}
	if len(all) == 0 {
		return CPUSnapshot{}, errors.New("no aggregate cpu line in /proc/stat")
	}
	per, err := cpu.Times(true)
	if err != nil {
		return CPUSnapshot{}, err
	}
	cur := all[0]

	s.mu.Lock()
	prevAll, prevPer := s.lastAll, s.lastPer
	s.lastAll, s.lastPer = &cur, per
	s.mu.Unlock()

	snap := CPUSnapshot{
		PerCorePercent: make([]float64, len(per)),
		Temperatures:   readTemperatures(),
		Freqs:          ReadCPUFreq(),
		TakenAt:        time.Now(),
	}
	if prevAll != nil {
		snap.Breakdown = diffCPUTimes(*prevAll, cur)
		snap.TotalPercent = busyPercent(snap.Breakdown)
	}
	// A core coming online or going offline changes the row count; skip that
	// one interval rather than pairing core N against a different core N.
	if len(prevPer) == len(per) {
		for i := range per {
			snap.PerCorePercent[i] = busyPercent(diffCPUTimes(prevPer[i], per[i]))
		}
	}
	return snap, nil
}

// busyPercent is the share of an interval that was neither idle nor blocked
// on I/O — the definition gopsutil's cpu.Percent uses on Linux, kept the same
// so the number on screen did not move when the sampler did.
func busyPercent(b CPUBreakdown) float64 {
	if !b.Valid {
		return 0
	}
	v := 100 - b.Idle - b.Iowait
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
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

	// ReadDir sorts as text, which reads thermal_zone10 before thermal_zone2.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "thermal_zone") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return zoneIndex(names[i]) < zoneIndex(names[j]) })

	var out []TempReading
	for _, name := range names {
		zpath := filepath.Join(thermalBase, name)

		label := name
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
//
// Guarded because they are process-wide and written from more than one
// goroutine: metrics_listen builds a second Model on the exporter's goroutine
// for every scrape, and NewModel applies the thresholds, while the TUI's
// renderer is reading them through TempColor.
var (
	tempMu   sync.RWMutex
	tempWarn float64 = 50 // yellow
	tempHot  float64 = 65 // orange
	tempCrit float64 = 80 // red
)

// SetTempThresholds overrides the warn/hot/crit boundaries used by TempColor.
func SetTempThresholds(warn, hot, crit float64) {
	tempMu.Lock()
	defer tempMu.Unlock()
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

// TempThresholds reports the active warn/hot/crit boundaries.
func TempThresholds() (warn, hot, crit float64) {
	tempMu.RLock()
	defer tempMu.RUnlock()
	return tempWarn, tempHot, tempCrit
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
func zoneIndex(name string) int { return suffixIndex(name, "thermal_zone") }

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
	warn, hot, crit := TempThresholds()
	switch {
	case t >= crit:
		return "196" // red
	case t >= hot:
		return "214" // orange
	case t >= warn:
		return "226" // yellow
	default:
		return "42" // green
	}
}

func FormatTemp(t float64) string {
	return fmt.Sprintf("%.1f°C", t)
}

// CheckTempSensors reports whether any temperature sensor is readable. The
// gopsutil hwmon walk is tried first, exactly as readTemperatures does, so a
// host that reports sensors without /sys/class/thermal still comes out
// present.
func CheckTempSensors() Capability {
	return checkTempSensors(readTemperatures())
}

// checkTempSensors is the pure half: the sensor walk is the host's answer,
// the verdict is this function's.
func checkTempSensors(temps []TempReading) Capability {
	c := Capability{
		Name:    "temperature sensors",
		Source:  thermalBase,
		Affects: "CPU tab temperatures, temperature alerts, trip-point calibration",
	}

	if len(temps) > 0 {
		labels := make([]string, 0, len(temps))
		for _, t := range temps {
			labels = append(labels, t.Label)
		}
		c.Status = CapPresent
		c.Detail = plural(len(temps), "sensor", "sensors") + ": " + firstN(labels, 4)
		return c
	}

	st, why := pathStatus(thermalBase)
	c.Status = st
	switch st {
	case CapPresent:
		// The directory is there but exposes no usable zone — a VM, or a
		// board whose sensors need a driver that is not loaded.
		c.Status = CapAbsent
		c.Detail = "no thermal zone or hwmon sensor reports a usable reading"
	case CapDenied:
		c.Detail = why
	default:
		c.Detail = why + " — no hwmon sensors either (common in a VM or container)"
	}
	return c
}
