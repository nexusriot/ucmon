package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

// exportSnapshot writes a plain-text report of the current readings to
// ~/ucmon-report-<timestamp>.txt and returns the path it wrote.
func (m Model) exportSnapshot() (string, error) {
	now := time.Now()
	report := m.buildReport(now)

	dir := m.cfg.ReportDir
	if dir == "" {
		var err error
		if dir, err = os.UserHomeDir(); err != nil || dir == "" {
			dir = "."
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("ucmon-report-%s.txt", now.Format("20060102-150405")))
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// buildReport renders the snapshot as ANSI-free text suitable for a file.
func (m Model) buildReport(now time.Time) string {
	var b strings.Builder
	hr := strings.Repeat("─", 56)

	fmt.Fprintf(&b, "ucmon %s — snapshot %s\n%s\n\n", version, now.Format("2006-01-02 15:04:05"), hr)

	b.WriteString("CPU\n")
	fmt.Fprintf(&b, "  total          %.1f%%\n", m.cpuSnap.TotalPercent)
	for i, p := range m.cpuSnap.PerCorePercent {
		fmt.Fprintf(&b, "  core %-2d        %.1f%%\n", i, p)
	}
	if bd := m.cpuSnap.Breakdown; bd.Valid {
		fmt.Fprintf(&b, "  time           user %.0f%%  sys %.0f%%  nice %.0f%%  iowait %.0f%%  irq %.0f%%  steal %.0f%%  idle %.0f%%\n",
			bd.User, bd.System, bd.Nice, bd.Iowait, bd.IRQ, bd.Steal, bd.Idle)
		if bd.Busiest != "" {
			fmt.Fprintf(&b, "  busiest        %s\n", bd.Busiest)
		}
	}
	if len(m.cpuSnap.Temperatures) > 0 {
		b.WriteString("  temperatures:\n")
		for _, t := range m.cpuSnap.Temperatures {
			fmt.Fprintf(&b, "    %-16s %s\n", t.Label, probe.FormatTemp(t.Temp))
		}
	}
	b.WriteString("\n")

	s := m.memSnap
	b.WriteString("Memory\n")
	fmt.Fprintf(&b, "  RAM            %s / %s (%.1f%%)\n",
		probe.HumanBytes(s.Used), probe.HumanBytes(s.Total), s.UsedPct)
	fmt.Fprintf(&b, "  available      %s\n", probe.HumanBytes(s.Available))
	if s.SwapTotal > 0 {
		fmt.Fprintf(&b, "  swap           %s / %s (%.1f%%)\n",
			probe.HumanBytes(s.SwapUsed), probe.HumanBytes(s.SwapTotal), s.SwapUsedPct)
	} else {
		b.WriteString("  swap           not configured\n")
	}
	b.WriteString("\n")

	if len(m.cpuSnap.Freqs) > 0 {
		b.WriteString("Frequency\n")
		for _, f := range m.cpuSnap.Freqs {
			fmt.Fprintf(&b, "  %-8s       %.0f MHz (%.0f–%.0f), governor %s\n",
				f.Policy, f.CurMHz, f.MinMHz, f.MaxMHz, f.Governor)
		}
		b.WriteString("\n")
	}

	if m.psi.Present {
		b.WriteString("Pressure (PSI, some avg10/avg60)\n")
		for _, res := range []struct {
			name string
			r    probe.PSIRes
		}{{"cpu", m.psi.CPU}, {"memory", m.psi.Mem}, {"io", m.psi.IO}} {
			if !res.r.HasSome {
				continue
			}
			line := fmt.Sprintf("  %-14s %.1f%% / %.1f%%", res.name, res.r.SomeAvg10, res.r.SomeAvg60)
			if res.r.HasFull {
				line += fmt.Sprintf("  (full %.1f%% / %.1f%%)", res.r.FullAvg10, res.r.FullAvg60)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	p := m.powerSnap
	b.WriteString("Power\n")
	fmt.Fprintf(&b, "  load (1/5/15)  %.2f / %.2f / %.2f  (%d cores)\n",
		p.Load.Load1, p.Load.Load5, p.Load.Load15, p.Load.CPUCount)
	if p.Battery.Present {
		bat := p.Battery
		charge := "n/a"
		if bat.Capacity >= 0 {
			charge = fmt.Sprintf("%d%%", bat.Capacity)
		}
		line := fmt.Sprintf("  battery        %s %s", charge, bat.Status)
		if bat.PowerW > 0 {
			line += fmt.Sprintf(", %.1f W", bat.PowerW)
		}
		if bat.TimeLeft > 0 {
			line += fmt.Sprintf(", %s left", bat.TimeLeft.Round(time.Minute))
		}
		if bat.HealthPct > 0 {
			line += fmt.Sprintf(", health %.0f%%", bat.HealthPct)
		}
		b.WriteString(line + "\n")
	}
	if p.ACKnown {
		fmt.Fprintf(&b, "  AC             %s\n", boolWord(p.ACOnline, "online", "offline"))
	}
	if p.Throttle.Available {
		fmt.Fprintf(&b, "  throttle       raw=0x%x%s\n", p.Throttle.Raw, throttleFlags(p.Throttle))
	}
	b.WriteString("\n")

	if len(m.disks) > 0 {
		b.WriteString("Disks\n")
		for _, d := range m.disks {
			fmt.Fprintf(&b, "  %-18s %s / %s (%.1f%%) %s\n",
				d.MountPoint, probe.HumanBytes(d.Used), probe.HumanBytes(d.Total), d.UsedPct, d.FSType)
		}
		b.WriteString("\n")
	}

	if len(m.storage) > 0 {
		b.WriteString("Storage health\n")
		for _, h := range m.storage {
			line := fmt.Sprintf("  %-10s %-5s %-18s", h.Device, h.Kind, trunc(h.Name, 18))
			if pct, ok := h.WearPct(); ok {
				line += fmt.Sprintf(" wear %3.0f%%", pct)
			} else {
				line += " wear n/a "
			}
			if lbl := h.EOLLabel(); lbl != "" {
				line += "  reserve " + lbl
			}
			if h.ReadOnly {
				line += "  READ-ONLY"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	up := m.upIfaces()
	if len(up) > 0 {
		b.WriteString("Network\n")
		for _, ii := range up {
			line := fmt.Sprintf("  %-10s ↓ %-12s ↑ %-12s",
				ii.Name, probe.HumanBytesPerSec(ii.RxBps), probe.HumanBytesPerSec(ii.TxBps))
			if ii.WiFi.Present {
				line += fmt.Sprintf("  wifi %.0f%% %.0f dBm", ii.WiFi.QualityPct, ii.WiFi.SignalDBm)
				if link, ok := m.iwLinks[ii.Name]; ok && link.SSID != "" {
					line += fmt.Sprintf("  ssid %q ch %d", link.SSID, link.Channel)
				}
			}
			if n := ii.BadFramesTotal(); n > 0 {
				line += fmt.Sprintf("  err/drop %d", n)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if m.modem.Present {
		b.WriteString("Modem\n")
		fmt.Fprintf(&b, "  state          %s\n", m.modem.State)
		if m.modem.Operator != "" {
			fmt.Fprintf(&b, "  operator       %s (%s)\n", m.modem.Operator, m.modem.AccessTech)
		}
		if m.modem.SignalPct >= 0 {
			line := fmt.Sprintf("  signal         %d%%", m.modem.SignalPct)
			if m.modem.HasRSSI {
				line += fmt.Sprintf("  %.0f dBm", m.modem.RSSIdBm)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(m.pings) > 0 {
		b.WriteString("Latency\n")
		for _, r := range m.pings {
			state := "lost"
			if r.OK {
				state = fmt.Sprintf("%.1f ms", float64(r.RTT.Microseconds())/1000)
			}
			line := fmt.Sprintf("  %-24s %s (%s)", r.Target+" ("+r.Addr+")", state, r.Method)
			if win := m.lossWin[r.Target]; len(win) > 0 {
				line += fmt.Sprintf(", loss %.0f%%/%d", lossPct(win), len(win))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(m.procs) > 0 {
		ps := make([]probe.ProcInfo, len(m.procs))
		copy(ps, m.procs)
		sort.Slice(ps, func(i, j int) bool { return ps[i].CPUPct > ps[j].CPUPct })
		if len(ps) > 10 {
			ps = ps[:10]
		}
		b.WriteString("Top processes (CPU)\n")
		fmt.Fprintf(&b, "  %-7s %-18s %7s %7s %12s\n", "PID", "NAME", "CPU%", "MEM%", "DISK I/O")
		for _, pr := range ps {
			io := "-"
			if pr.IOKnown {
				io = probe.HumanBytesPerSec(pr.IOReadBps + pr.IOWriteBps)
			}
			fmt.Fprintf(&b, "  %-7d %-18s %6.1f%% %6.1f%% %12s\n",
				pr.PID, trunc(pr.Name, 18), pr.CPUPct, pr.MemPct, io)
		}
		b.WriteString("\n")
	}

	b.WriteString("Session peaks\n")
	fmt.Fprintf(&b, "  cpu            %.1f%%\n", m.peaks.cpuPct)
	if m.peaks.tempC > 0 {
		fmt.Fprintf(&b, "  temperature    %s (%s)\n", probe.FormatTemp(m.peaks.tempC), m.peaks.tempLabel)
	}
	fmt.Fprintf(&b, "  memory         %.1f%%\n", m.peaks.memPct)
	fmt.Fprintf(&b, "  load1          %.2f\n", m.peaks.load1)
	if m.peaks.battMin >= 0 {
		fmt.Fprintf(&b, "  battery min    %d%%\n", m.peaks.battMin)
	}
	b.WriteString("\n")

	b.WriteString("Alerts\n")
	if act := m.alerts.activeList(); len(act) > 0 {
		b.WriteString("  active:\n")
		for _, a := range act {
			fmt.Fprintf(&b, "    [%s] %s\n", a.sev.label(), a.msg)
		}
	} else {
		b.WriteString("  active: none\n")
	}
	if len(m.alerts.log) > 0 {
		b.WriteString("  recent log:\n")
		log := m.alerts.log
		start := 0
		if len(log) > 20 {
			start = len(log) - 20
		}
		for _, en := range log[start:] {
			state := en.sev.label()
			if en.cleared {
				state = "OK"
			}
			fmt.Fprintf(&b, "    %s [%-4s] %s\n", en.when.Format("15:04:05"), state, en.msg)
		}
	}

	return b.String()
}

func boolWord(v bool, t, f string) string {
	if v {
		return t
	}
	return f
}

func throttleFlags(t probe.ThrottleInfo) string {
	var f []string
	if t.UnderVoltageNow {
		f = append(f, "under-voltage")
	}
	if t.FreqCappedNow {
		f = append(f, "freq-capped")
	}
	if t.ThrottledNow {
		f = append(f, "throttled")
	}
	if t.SoftTempLimitNow {
		f = append(f, "soft-temp-limit")
	}
	if len(f) == 0 {
		return " (ok)"
	}
	return " (" + strings.Join(f, ", ") + ")"
}
