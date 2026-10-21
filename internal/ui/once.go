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
	return collectOnce(cfg).buildReport(time.Now()), nil
}

// collectOnce primes the delta-based samplers, waits a short window, then
// reads everything once. Every probe is best-effort — an absent source is a
// hidden row, not a failure — so there is nothing here for it to report.
func collectOnce(cfg config.Config) Model {
	cfg.Sanitize()
	m := NewModel(cfg) // NewModel sanitizes, calibrates and applies thresholds
	cfg = m.cfg

	// Prime the delta-based samplers.
	_, _ = m.cpuSampler.Sample()
	_, _ = m.netSampler.Sample()
	_, _, _ = m.diskSampler.Sample()
	_, _ = m.procSampler.List()

	time.Sleep(600 * time.Millisecond)

	if snap, err := m.cpuSampler.Sample(); err == nil {
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
	m.storage = probe.ReadStorageHealth()
	if cfg.ModemEnabled {
		m.modem = probe.ReadModem()
	}
	m.gpuSnap = probe.SampleGPU()
	m.backlight = probe.ReadBacklight(cfg.BacklightDevice)
	m.iwLinks = probe.ReadIWLinks(func(n string) bool { return !cfg.HideIface(n) })

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
	m.recordTrend(time.Now())

	return m
}

// RunOnceJSON is RunOnce in the machine-readable shape, for scripts and cron
// jobs that would otherwise have to re-parse the text report's layout every
// time the layout changes.
func RunOnceJSON(cfg config.Config) (string, error) {
	return collectOnce(cfg).buildJSONReport(time.Now())
}
