package ui

import (
	"fmt"

	"github.com/nexusriot/ucmon/internal/probe"
)

// sessionPeaks tracks the worst readings seen since ucmon started, so a
// glance answers "did it throttle / fill up / drain while I was away".
type sessionPeaks struct {
	cpuPct    float64
	tempC     float64
	tempLabel string
	load1     float64
	memPct    float64
	battMin   int // -1 until a battery reading is seen
}

func newSessionPeaks() sessionPeaks {
	return sessionPeaks{battMin: -1}
}

func (p *sessionPeaks) observeCPU(total float64, temps []probe.TempReading) {
	if total > p.cpuPct {
		p.cpuPct = total
	}
	for _, t := range temps {
		if t.Temp > p.tempC {
			p.tempC = t.Temp
			p.tempLabel = t.Label
		}
	}
}

func (p *sessionPeaks) observeMem(usedPct float64) {
	if usedPct > p.memPct {
		p.memPct = usedPct
	}
}

func (p *sessionPeaks) observeLoad(load1 float64) {
	if load1 > p.load1 {
		p.load1 = load1
	}
}

func (p *sessionPeaks) observeBattery(capacity int) {
	if capacity < 0 {
		return
	}
	if p.battMin < 0 || capacity < p.battMin {
		p.battMin = capacity
	}
}

// tempSummary renders "71.2°C (cpu_thermal)" or "" before any reading.
func (p sessionPeaks) tempSummary() string {
	if p.tempC <= 0 {
		return ""
	}
	return fmt.Sprintf("%s (%s)", probe.FormatTemp(p.tempC), p.tempLabel)
}
