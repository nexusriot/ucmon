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

	dir, err := os.UserHomeDir()
	if err != nil || dir == "" {
		dir = "."
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

	// CPU & temperatures.
	b.WriteString("CPU\n")
	fmt.Fprintf(&b, "  total          %.1f%%\n", m.cpuSnap.TotalPercent)
	for i, p := range m.cpuSnap.PerCorePercent {
		fmt.Fprintf(&b, "  core %-2d        %.1f%%\n", i, p)
	}
	if len(m.cpuSnap.Temperatures) > 0 {
		b.WriteString("  temperatures:\n")
		for _, t := range m.cpuSnap.Temperatures {
			fmt.Fprintf(&b, "    %-16s %s\n", t.Label, probe.FormatTemp(t.Temp))
		}
	}
	b.WriteString("\n")

	// Memory.
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

	// Power: load, battery, throttle.
	p := m.powerSnap
	b.WriteString("Power\n")
	fmt.Fprintf(&b, "  load (1/5/15)  %.2f / %.2f / %.2f  (%d cores)\n",
		p.Load.Load1, p.Load.Load5, p.Load.Load15, p.Load.CPUCount)
	if p.Battery.Present {
		bat := p.Battery
		line := fmt.Sprintf("  battery        %d%% %s", bat.Capacity, bat.Status)
		if bat.PowerW > 0 {
			line += fmt.Sprintf(", %.1f W", bat.PowerW)
		}
		if bat.TimeLeft > 0 {
			line += fmt.Sprintf(", %s left", bat.TimeLeft.Round(time.Minute))
		}
		b.WriteString(line + "\n")
	}
	if p.ACKnown {
		fmt.Fprintf(&b, "  AC            %s\n", boolWord(p.ACOnline, "online", "offline"))
	}
	if p.Throttle.Available {
		fmt.Fprintf(&b, "  throttle       raw=0x%x%s\n", p.Throttle.Raw, throttleFlags(p.Throttle))
	}
	b.WriteString("\n")

	// Filesystems.
	if len(m.disks) > 0 {
		b.WriteString("Disks\n")
		for _, d := range m.disks {
			fmt.Fprintf(&b, "  %-18s %s / %s (%.1f%%) %s\n",
				d.MountPoint, probe.HumanBytes(d.Used), probe.HumanBytes(d.Total), d.UsedPct, d.FSType)
		}
		b.WriteString("\n")
	}

	// Network interfaces (up only).
	up := m.upIfaces()
	if len(up) > 0 {
		b.WriteString("Network\n")
		for _, ii := range up {
			fmt.Fprintf(&b, "  %-10s ↓ %-12s ↑ %-12s\n",
				ii.Name, probe.HumanBytesPerSec(ii.RxBps), probe.HumanBytesPerSec(ii.TxBps))
		}
		b.WriteString("\n")
	}

	// Top processes by CPU.
	if len(m.procs) > 0 {
		ps := make([]probe.ProcInfo, len(m.procs))
		copy(ps, m.procs)
		sort.Slice(ps, func(i, j int) bool { return ps[i].CPUPct > ps[j].CPUPct })
		if len(ps) > 10 {
			ps = ps[:10]
		}
		b.WriteString("Top processes (CPU)\n")
		fmt.Fprintf(&b, "  %-7s %-18s %7s %7s\n", "PID", "NAME", "CPU%", "MEM%")
		for _, pr := range ps {
			fmt.Fprintf(&b, "  %-7d %-18s %6.1f%% %6.1f%%\n",
				pr.PID, truncName(pr.Name, 18), pr.CPUPct, pr.MemPct)
		}
		b.WriteString("\n")
	}

	// Alerts.
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

func truncName(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}
