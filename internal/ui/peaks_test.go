package ui

import (
	"testing"

	"github.com/nexusriot/ucmon/internal/probe"
)

func TestSessionPeaks(t *testing.T) {
	p := newSessionPeaks()

	p.observeCPU(40, []probe.TempReading{{Label: "cpu", Temp: 55}})
	p.observeCPU(90, []probe.TempReading{{Label: "soc", Temp: 71}})
	p.observeCPU(60, []probe.TempReading{{Label: "cpu", Temp: 50}})
	if p.cpuPct != 90 {
		t.Errorf("cpuPct = %v", p.cpuPct)
	}
	if p.tempC != 71 || p.tempLabel != "soc" {
		t.Errorf("temp peak = %v (%s)", p.tempC, p.tempLabel)
	}

	p.observeMem(50)
	p.observeMem(45)
	if p.memPct != 50 {
		t.Errorf("memPct = %v", p.memPct)
	}

	p.observeLoad(1.5)
	p.observeLoad(0.5)
	if p.load1 != 1.5 {
		t.Errorf("load1 = %v", p.load1)
	}

	if p.battMin != -1 {
		t.Errorf("battMin should start unseen, got %d", p.battMin)
	}
	p.observeBattery(80)
	p.observeBattery(35)
	p.observeBattery(60)
	p.observeBattery(-1) // unknown readings are ignored
	if p.battMin != 35 {
		t.Errorf("battMin = %d", p.battMin)
	}

	if s := p.tempSummary(); s == "" {
		t.Error("tempSummary empty after observations")
	}
}
