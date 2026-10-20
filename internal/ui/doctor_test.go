package ui

import (
	"strings"
	"testing"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func sampleCaps() []probe.Capability {
	return []probe.Capability{
		{
			Name:    "pressure stall (PSI)",
			Source:  "/proc/pressure",
			Status:  probe.CapPresent,
			Detail:  "cpu/memory/io readable",
			Affects: "Memory tab pressure rows",
		},
		{
			Name:    "GPU / VideoCore (vcgencmd)",
			Source:  "/usr/bin/vcgencmd, $PATH",
			Status:  probe.CapAbsent,
			Detail:  "vcgencmd not found",
			Affects: "the whole GPU tab",
			Fix:     "sudo apt install raspberrypi-utils",
		},
		{
			Name:    "per-process disk I/O",
			Source:  "/proc/<pid>/io",
			Status:  probe.CapDenied,
			Detail:  "own processes only",
			Affects: "the Processes tab I/O columns",
			Fix:     "sudo setcap cap_sys_ptrace+ep $(command -v ucmon)",
		},
		{
			Name:    "backlight brightness",
			Source:  "/sys/class/backlight/panel0/brightness",
			Status:  probe.CapReadOnly,
			Detail:  "not writable by this user",
			Affects: "the GPU tab's +/- keys",
			Fix:     probe.BacklightUdevRule,
		},
	}
}

// The report has to answer the question it exists for: what is missing, from
// which screen, and what to do about it.
func TestDoctorReportNamesEverySource(t *testing.T) {
	out := doctorReport(sampleCaps(), config.Default(), "/etc/ucmon.toml")

	for _, want := range []string{
		"ucmon " + version,
		"[present]", "[absent]", "[denied]", "[read-only]",
		"pressure stall (PSI)", "GPU / VideoCore (vcgencmd)",
		"/proc/<pid>/io", "the whole GPU tab",
		"4 sources: 1 present, 1 absent, 1 denied, 1 read-only",
		"/etc/ucmon.toml",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q\n%s", want, out)
		}
	}

	// "hides" belongs to the entries that are missing; a present source has
	// nothing to explain.
	psi, _, _ := strings.Cut(out, "[absent]")
	if strings.Contains(psi, "hides") {
		t.Errorf("a present capability should not list what it hides:\n%s", psi)
	}
}

// The udev rule is the whole point of reporting a read-only source, so it has
// to survive formatting line for line.
func TestDoctorReportPrintsTheUdevRule(t *testing.T) {
	out := doctorReport(sampleCaps(), config.Default(), "")

	for _, line := range strings.Split(probe.BacklightUdevRule, "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(out, line) {
			t.Errorf("udev rule line missing from the report: %q", line)
		}
	}
	if strings.Contains(out, " \n") {
		t.Error("report contains a line padded with trailing whitespace")
	}
	if !strings.Contains(out, "(none — built-in defaults)") {
		t.Error("an empty config path should be reported as such")
	}
}

// Half of "why is that empty" is the config, which no capability check can
// see: a switch set to false, a glob hiding the row, or a key that was never
// recognised in the first place.
func TestDoctorReportConfigSection(t *testing.T) {
	cfg := config.Default()
	cfg.PingEnabled = false
	cfg.NetIgnore = []string{"veth*", "docker*"}
	cfg.Unknown = []config.UnknownKey{{Key: "temp_warnn", Line: 4}}

	out := doctorReport(nil, cfg, "/etc/ucmon.toml")
	for _, want := range []string{
		"ping_enabled", "no latency block",
		"net_ignore", "veth*, docker*",
		"temp_warnn (line 4)", "--check-config",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("config section is missing %q\n%s", want, out)
		}
	}

	// modem polling is off by default, which is itself a reason a block is
	// empty, so "nothing is suppressing anything" needs it switched on.
	all := config.Default()
	all.ModemEnabled = true
	clean := doctorReport(nil, all, "")
	if !strings.Contains(clean, "nothing in the config is suppressing a reading") {
		t.Errorf("a default config should say so:\n%s", clean)
	}
}

// Doctor must not need a terminal, a config file or any privilege: it is the
// command people run when nothing else works.
func TestDoctorRunsAgainstTheRealHost(t *testing.T) {
	out := Doctor(config.Default(), "")
	if !strings.Contains(out, "capability report") {
		t.Fatalf("unexpected report:\n%s", out)
	}
	for _, name := range []string{"cpufreq policies", "backlight brightness", "unprivileged ICMP ping"} {
		if !strings.Contains(out, name) {
			t.Errorf("real-host report is missing %q", name)
		}
	}
}
