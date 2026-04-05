package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/sensors"
)

type CPUSnapshot struct {
	PerCorePercent []float64
	TotalPercent   float64
	Temperatures   []TempReading
	TakenAt        time.Time
}

type TempReading struct {
	Label string
	Temp  float64
}

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
		Temperatures:   temps,
		TakenAt:        time.Now(),
	}, nil
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

func readThermalZones() []TempReading {
	const base = "/sys/class/thermal"
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}

	var out []TempReading
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		zpath := filepath.Join(base, e.Name())

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

func TempColor(t float64) string {
	switch {
	case t >= 80:
		return "196" // red
	case t >= 65:
		return "214" // orange
	case t >= 50:
		return "226" // yellow
	default:
		return "42" // green
	}
}

func FormatTemp(t float64) string {
	return fmt.Sprintf("%.1f°C", t)
}
