package ui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// Doctor renders the capability report behind `ucmon doctor`: every source
// ucmon reads, whether this host offers it, and what is missing from the
// screen when it does not. The probes decide; this layer only formats, so
// the report cannot disagree with the behaviour it explains.
func Doctor(cfg config.Config, cfgPath string) string {
	return doctorReport(probe.Capabilities(cfg.BacklightDevice), cfg, cfgPath)
}

// doctorIndent aligns every continuation line under the capability name,
// past the widest status label ("[read-only]").
const doctorIndent = "             "

func doctorReport(caps []probe.Capability, cfg config.Config, cfgPath string) string {
	var b strings.Builder
	hr := strings.Repeat("─", 72)

	fmt.Fprintf(&b, "ucmon %s — capability report\n%s\n", version, hr)
	b.WriteString("What this host can measure, and why the rest of the screen is empty.\n\n")

	counts := map[probe.CapStatus]int{}
	for _, c := range caps {
		counts[c.Status]++

		fmt.Fprintf(&b, "%-12s %s\n", "["+c.Status.String()+"]", c.Name)
		doctorField(&b, "source", c.Source)
		if c.OK() {
			doctorField(&b, "found", c.Detail)
		} else {
			doctorField(&b, "why", c.Detail)
			doctorField(&b, "hides", c.Affects)
		}
		doctorField(&b, "fix", c.Fix)
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "%s\n%d sources: %d present, %d absent, %d denied, %d read-only\n",
		hr, len(caps), counts[probe.CapPresent], counts[probe.CapAbsent],
		counts[probe.CapDenied], counts[probe.CapReadOnly])
	b.WriteString("An absent source is not a failure: ucmon hides the row it would have\n" +
		"filled and keeps every other reading.\n")

	b.WriteString("\n")
	doctorConfig(&b, cfg, cfgPath)
	return b.String()
}

// doctorField writes one labelled line, indenting the continuation lines of a
// multi-line value (the udev rules) to the same column.
func doctorField(b *strings.Builder, label, val string) {
	if val == "" {
		return
	}
	pad := doctorIndent + strings.Repeat(" ", 8)
	for i, line := range strings.Split(val, "\n") {
		if i == 0 {
			fmt.Fprintf(b, "%s%-8s%s\n", doctorIndent, label, line)
			continue
		}
		if line == "" {
			b.WriteString("\n")
			continue
		}
		fmt.Fprintf(b, "%s%s\n", pad, line)
	}
}

// doctorConfig reports the settings that suppress a reading the host could
// otherwise produce — the other half of "why is that empty", and the one no
// capability check can see.
func doctorConfig(b *strings.Builder, cfg config.Config, cfgPath string) {
	b.WriteString("Config\n")
	if cfgPath == "" {
		fmt.Fprintf(b, "%-16s (none — built-in defaults)\n", "  file")
	} else if _, err := os.Stat(cfgPath); err != nil {
		fmt.Fprintf(b, "%-16s %s (not present — built-in defaults)\n", "  file", cfgPath)
	} else {
		fmt.Fprintf(b, "%-16s %s\n", "  file", cfgPath)
	}
	if len(cfg.Unknown) > 0 {
		var parts []string
		for _, u := range cfg.Unknown {
			parts = append(parts, fmt.Sprintf("%s (line %d)", u.Key, u.Line))
		}
		fmt.Fprintf(b, "%-16s %s — ignored; run ucmon --check-config\n",
			"  unknown keys", strings.Join(parts, ", "))
	}

	off := map[string]string{}
	if !cfg.PingEnabled {
		off["ping_enabled"] = "false — the Network tab shows no latency block"
	}
	if !cfg.ModemEnabled {
		off["modem_enabled"] = "false — ModemManager is never polled"
	}
	if !cfg.AlertsEnabled {
		off["alerts_enabled"] = "false — no banner, no Alerts tab entries"
	}
	if len(cfg.NetIgnore) > 0 {
		off["net_ignore"] = strings.Join(cfg.NetIgnore, ", ") + " — matching interfaces are hidden"
	}
	if len(cfg.DiskIgnore) > 0 {
		off["disk_ignore"] = strings.Join(cfg.DiskIgnore, ", ") + " — matching mounts are hidden"
	}
	if len(cfg.TempIgnore) > 0 {
		off["temp_ignore"] = strings.Join(cfg.TempIgnore, ", ") + " — matching sensors are hidden"
	}
	keys := make([]string, 0, len(off))
	for k := range off {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "%-16s %s\n", "  "+k, off[k])
	}
	if len(keys) == 0 && len(cfg.Unknown) == 0 {
		b.WriteString("  nothing in the config is suppressing a reading\n")
	}
}
