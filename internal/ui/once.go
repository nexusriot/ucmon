package ui

import (
	"time"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// RunOnce collects a single snapshot of every reading and returns the same
// plain-text report the `w` key writes, for --once / headless use. Rate-based
// probes (CPU%, network and disk throughput) are primed, then re-sampled
// after a short window so the report shows real rates instead of zeros.
func RunOnce(cfg config.Config) (string, error) {
	cfg.Sanitize()
	probe.SetTempThresholds(cfg.TempWarn, cfg.TempHot, cfg.TempCrit)
	m := NewModel(cfg)

	// Prime the delta-based samplers.
	_, _ = probe.SampleCPU()
	_, _ = m.netSampler.Sample()
	_, _, _ = m.diskSampler.Sample()
	_, _ = m.procSampler.List()

	time.Sleep(600 * time.Millisecond)

	if snap, err := probe.SampleCPU(); err == nil {
		snap.Temperatures = m.keepTemps(snap.Temperatures)
		m.cpuSnap = snap
		m.peaks.observeCPU(snap.TotalPercent, snap.Temperatures)
	}
	if snap, err := probe.SampleMem(); err == nil {
		m.memSnap = snap
		m.peaks.observeMem(snap.UsedPct)
	}
	if snap, err := probe.SamplePower(); err == nil {
		m.powerSnap = snap
		m.peaks.observeLoad(snap.Load.Load1)
		m.peaks.observeBattery(snap.Battery.Capacity)
	}
	if disks, ios, err := m.diskSampler.Sample(); err == nil {
		m.disks, m.diskIOs = m.keepDisks(disks), ios
	}
	if snap, err := m.netSampler.Sample(); err == nil {
		snap.Ifaces = m.keepIfaces(snap.Ifaces)
		m.netSnap = snap
	}
	if procs, err := m.procSampler.List(); err == nil {
		m.procs = procs
	}
	if conns, err := probe.ListConnections(); err == nil {
		m.conns = conns
	}
	m.psi = probe.ReadPSI()
	m.gpuSnap = probe.SampleGPU()
	m.backlight = probe.ReadBacklight(cfg.BacklightDevice)
	m.iwLinks = probe.ReadIWLinks()

	if m.latSampler != nil {
		results := m.latSampler.Sample(1 * time.Second)
		m.pings = results
		for _, r := range results {
			m.lossWin[r.Target] = append(m.lossWin[r.Target], r.OK)
		}
	}

	if cfg.AlertsEnabled {
		m.alerts.update(m.currentAlertConds(), time.Now())
	}

	return m.buildReport(time.Now()), nil
}
