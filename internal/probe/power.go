package probe

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/load"
)

type LoadInfo struct {
	Load1, Load5, Load15 float64
	CPUCount             int
}

type BatteryInfo struct {
	Present    bool
	Name       string
	Capacity   int    // percent, -1 if unknown
	Status     string // Charging / Discharging / Full / Not charging / Unknown
	VoltageV   float64
	PowerW     float64       // instantaneous draw/charge in watts, 0 if unknown
	TimeLeft   time.Duration // to empty (discharging) or to full (charging)
	Technology string
}

type ThrottleInfo struct {
	Available             bool
	Raw                   uint64
	UnderVoltageNow       bool
	FreqCappedNow         bool
	ThrottledNow          bool
	SoftTempLimitNow      bool
	UnderVoltageOccurred  bool
	FreqCappedOccurred    bool
	ThrottledOccurred     bool
	SoftTempLimitOccurred bool
}

type PowerSnapshot struct {
	Load     LoadInfo
	ACKnown  bool
	ACOnline bool
	Battery  BatteryInfo
	Throttle ThrottleInfo
	TakenAt  time.Time
}

func SamplePower() (PowerSnapshot, error) {
	snap := PowerSnapshot{TakenAt: time.Now()}

	if avg, err := load.Avg(); err == nil && avg != nil {
		snap.Load = LoadInfo{
			Load1:    avg.Load1,
			Load5:    avg.Load5,
			Load15:   avg.Load15,
			CPUCount: runtime.NumCPU(),
		}
	} else {
		snap.Load.CPUCount = runtime.NumCPU()
	}

	snap.Battery, snap.ACKnown, snap.ACOnline = readPowerSupply()
	snap.Throttle = readThrottle()

	return snap, nil
}

const powerSupplyBase = "/sys/class/power_supply"

func sysReadStr(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func sysReadInt(path string) (int64, bool) {
	s, ok := sysReadStr(path)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// readPowerSupply walks /sys/class/power_supply and returns the first battery
// found plus whether an AC/USB mains source is online.
func readPowerSupply() (bat BatteryInfo, acKnown bool, acOnline bool) {
	bat.Capacity = -1

	entries, err := os.ReadDir(powerSupplyBase)
	if err != nil {
		return bat, false, false
	}

	for _, e := range entries {
		dir := filepath.Join(powerSupplyBase, e.Name())
		typ, _ := sysReadStr(filepath.Join(dir, "type"))

		switch typ {
		case "Mains", "USB":
			if v, ok := sysReadInt(filepath.Join(dir, "online")); ok {
				acKnown = true
				if v == 1 {
					acOnline = true
				}
			}
		case "Battery":
			if bat.Present {
				continue // keep the first battery
			}
			bat.Present = true
			bat.Name = e.Name()

			if v, ok := sysReadInt(filepath.Join(dir, "capacity")); ok {
				bat.Capacity = int(v)
			}
			if s, ok := sysReadStr(filepath.Join(dir, "status")); ok {
				bat.Status = s
			}
			if s, ok := sysReadStr(filepath.Join(dir, "technology")); ok {
				bat.Technology = s
			}
			if v, ok := sysReadInt(filepath.Join(dir, "voltage_now")); ok {
				bat.VoltageV = float64(v) / 1e6 // µV -> V
			}

			bat.PowerW, bat.TimeLeft = batteryRate(dir, bat.Status)
		}
	}

	return bat, acKnown, acOnline
}

// batteryRate computes instantaneous power (W) and an estimated time to
// empty (discharging) or to full (charging) from sysfs energy/charge counters.
func batteryRate(dir, status string) (powerW float64, timeLeft time.Duration) {
	// Prefer energy (µWh) / power (µW); fall back to charge (µAh) / current (µA).
	if powerNow, ok := sysReadInt(filepath.Join(dir, "power_now")); ok && powerNow != 0 {
		p := float64(absI64(powerNow))
		powerW = p / 1e6
		energyNow, ok1 := sysReadInt(filepath.Join(dir, "energy_now"))
		energyFull, ok2 := sysReadInt(filepath.Join(dir, "energy_full"))
		timeLeft = estimate(status, energyNow, energyFull, ok1, ok2, p)
		return powerW, timeLeft
	}

	if currentNow, ok := sysReadInt(filepath.Join(dir, "current_now")); ok && currentNow != 0 {
		i := float64(absI64(currentNow))
		if v, ok := sysReadInt(filepath.Join(dir, "voltage_now")); ok {
			powerW = (i / 1e6) * (float64(v) / 1e6)
		}
		chargeNow, ok1 := sysReadInt(filepath.Join(dir, "charge_now"))
		chargeFull, ok2 := sysReadInt(filepath.Join(dir, "charge_full"))
		timeLeft = estimate(status, chargeNow, chargeFull, ok1, ok2, i)
	}

	return powerW, timeLeft
}

func estimate(status string, now, full int64, okNow, okFull bool, rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	switch status {
	case "Discharging":
		if okNow && now > 0 {
			return time.Duration(float64(now) / rate * float64(time.Hour))
		}
	case "Charging":
		if okNow && okFull && full > now && now >= 0 {
			return time.Duration(float64(full-now) / rate * float64(time.Hour))
		}
	}
	return 0
}

func absI64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func vcgencmdPath() string {
	for _, p := range []string{"/usr/bin/vcgencmd", "/opt/vc/bin/vcgencmd"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("vcgencmd"); err == nil {
		return p
	}
	return ""
}

// readThrottle parses `vcgencmd get_throttled` (Raspberry Pi / uConsole CM4).
// Absence of vcgencmd is reported via Available=false, not an error.
func readThrottle() ThrottleInfo {
	bin := vcgencmdPath()
	if bin == "" {
		return ThrottleInfo{}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "get_throttled").Output()
	if err != nil {
		return ThrottleInfo{}
	}

	// Expected: "throttled=0x50005"
	s := strings.TrimSpace(string(out))
	idx := strings.Index(s, "0x")
	if idx < 0 {
		return ThrottleInfo{}
	}
	raw, err := strconv.ParseUint(s[idx+2:], 16, 64)
	if err != nil {
		return ThrottleInfo{}
	}

	return ThrottleInfo{
		Available:             true,
		Raw:                   raw,
		UnderVoltageNow:       raw&(1<<0) != 0,
		FreqCappedNow:         raw&(1<<1) != 0,
		ThrottledNow:          raw&(1<<2) != 0,
		SoftTempLimitNow:      raw&(1<<3) != 0,
		UnderVoltageOccurred:  raw&(1<<16) != 0,
		FreqCappedOccurred:    raw&(1<<17) != 0,
		ThrottledOccurred:     raw&(1<<18) != 0,
		SoftTempLimitOccurred: raw&(1<<19) != 0,
	}
}
