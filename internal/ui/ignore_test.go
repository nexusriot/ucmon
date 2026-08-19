package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func ignoreModel(t *testing.T, apply func(*config.Config)) Model {
	t.Helper()
	cfg := config.Default()
	apply(&cfg)
	m := NewModel(cfg)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return mm.(Model)
}

// Filtering happens where the snapshot lands, so a hidden interface leaves no
// trace anywhere downstream — not in the view, not in the throughput history,
// and not in the alert engine.
func TestIgnoredIfacesVanishEverywhere(t *testing.T) {
	m := ignoreModel(t, func(c *config.Config) {
		c.NetIgnore = []string{"veth*", "br-*", "docker0"}
		c.WifiLowDBm = -70
	})
	m.activeTab = tabNet

	snap := probe.NetSnapshot{TakenAt: nonZeroTime(), Ifaces: []probe.IfaceInfo{
		{Name: "wlan0", IsUp: true, RxBps: 2048, TxBps: 512,
			WiFi: probe.WiFiInfo{Present: true, QualityPct: 20, SignalDBm: -85}},
		{Name: "docker0", IsUp: true, RxBps: 1, TxBps: 1},
		{Name: "veth785acc5", IsUp: true, RxBps: 1, TxBps: 1},
		{Name: "br-14302e83", IsUp: true, RxBps: 1, TxBps: 1},
		{Name: "eth0", IsUp: true},
	}}
	// A hidden interface with a weak signal must not raise an alert either.
	snap.Ifaces[1].WiFi = probe.WiFiInfo{Present: true, QualityPct: 5, SignalDBm: -90}

	mm, _ := m.Update(netMsg(snap))
	m = mm.(Model)

	if got := len(m.upIfaces()); got != 2 {
		t.Errorf("upIfaces = %d, want 2 (wlan0, eth0)", got)
	}
	for _, hidden := range []string{"docker0", "veth785acc5", "br-14302e83"} {
		if _, ok := m.rxHist[hidden]; ok {
			t.Errorf("%s kept a history buffer", hidden)
		}
		if strings.Contains(m.viewNet(), hidden) {
			t.Errorf("%s is still rendered", hidden)
		}
	}
	if _, ok := m.rxHist["wlan0"]; !ok {
		t.Error("a visible interface lost its history")
	}

	conds := m.currentAlertConds()
	if _, ok := conds["wifi:docker0"]; ok {
		t.Error("a hidden interface raised a WiFi alert")
	}
	if _, ok := conds["wifi:wlan0"]; !ok {
		t.Error("a visible weak-signal interface stopped alerting")
	}
}

// Same for mounts: hidden filesystems leave the Disk tab, the report and the
// disk-full alert together.
func TestIgnoredMountsVanishEverywhere(t *testing.T) {
	m := ignoreModel(t, func(c *config.Config) {
		c.DiskIgnore = []string{"/snap/*", "/boot/efi"}
	})
	m.activeTab = tabDisk

	mm, _ := m.Update(diskMsg{disks: []probe.DiskInfo{
		{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4", UsedPct: 50, Total: 1 << 34},
		{Device: "/dev/loop0", MountPoint: "/snap/core22/1621", FSType: "squashfs", UsedPct: 100, Total: 1 << 26},
		{Device: "/dev/mmcblk0p1", MountPoint: "/boot/efi", FSType: "vfat", UsedPct: 99, Total: 1 << 28},
	}})
	m = mm.(Model)

	if len(m.disks) != 1 || m.disks[0].MountPoint != "/" {
		t.Fatalf("disks = %+v, want only /", m.disks)
	}
	view := m.viewDisk()
	for _, hidden := range []string{"/snap/core22", "/boot/efi"} {
		if strings.Contains(view, hidden) {
			t.Errorf("%s is still rendered", hidden)
		}
	}
	if !strings.Contains(m.buildReport(time.Now()), "/dev/mmcblk0p2") &&
		!strings.Contains(m.buildReport(time.Now()), " / ") {
		t.Error("the visible filesystem left the report")
	}
	if _, ok := m.currentAlertConds()["disk:/boot/efi"]; ok {
		t.Error("a hidden filesystem raised a disk alert")
	}
}

// And sensors: a hidden reading must not colour the CPU tab, accumulate
// history, move the session peak, or raise a temperature alert.
func TestIgnoredSensorsVanishEverywhere(t *testing.T) {
	m := ignoreModel(t, func(c *config.Config) {
		c.TempIgnore = []string{"coretemp_core_*"}
	})
	m.activeTab = tabCPU

	mm, _ := m.Update(cpuMsg(probe.CPUSnapshot{
		TakenAt:      nonZeroTime(),
		TotalPercent: 10,
		Temperatures: []probe.TempReading{
			{Label: "cpu_thermal", Temp: 55},
			{Label: "coretemp_core_8", Temp: 95}, // above temp_crit
			{Label: "coretemp_core_12", Temp: 92},
		},
	}))
	m = mm.(Model)

	if len(m.cpuSnap.Temperatures) != 1 {
		t.Fatalf("temperatures = %+v, want only cpu_thermal", m.cpuSnap.Temperatures)
	}
	if _, ok := m.tempHist["coretemp_core_8"]; ok {
		t.Error("a hidden sensor kept history")
	}
	if m.peaks.tempC >= 90 {
		t.Errorf("a hidden sensor set the session peak to %.0f", m.peaks.tempC)
	}
	if strings.Contains(m.viewCPU(), "coretemp") {
		t.Error("a hidden sensor is still rendered")
	}
	if _, ok := m.currentAlertConds()["temp:coretemp_core_8"]; ok {
		t.Error("a hidden sensor raised a temperature alert")
	}
}

// With no patterns configured nothing is filtered and no copying happens.
func TestNoIgnorePatternsKeepsEverything(t *testing.T) {
	m := testModel()
	in := []probe.IfaceInfo{{Name: "veth0"}, {Name: "wlan0"}}
	if got := m.keepIfaces(in); len(got) != 2 {
		t.Errorf("keepIfaces dropped rows with no patterns: %+v", got)
	}
	disks := []probe.DiskInfo{{MountPoint: "/snap/x"}, {MountPoint: "/"}}
	if got := m.keepDisks(disks); len(got) != 2 {
		t.Errorf("keepDisks dropped rows with no patterns: %+v", got)
	}
	temps := []probe.TempReading{{Label: "coretemp_core_1"}, {Label: "cpu_thermal"}}
	if got := m.keepTemps(temps); len(got) != 2 {
		t.Errorf("keepTemps dropped rows with no patterns: %+v", got)
	}
}

// Filtering must not scribble over the caller's slice.
func TestFiltersDoNotAliasInput(t *testing.T) {
	m := ignoreModel(t, func(c *config.Config) { c.NetIgnore = []string{"veth*"} })
	in := []probe.IfaceInfo{{Name: "veth0"}, {Name: "wlan0"}, {Name: "veth1"}}
	got := m.keepIfaces(in)

	if len(got) != 1 || got[0].Name != "wlan0" {
		t.Fatalf("keepIfaces = %+v", got)
	}
	if in[0].Name != "veth0" || in[1].Name != "wlan0" || in[2].Name != "veth1" {
		t.Errorf("input slice was mutated: %+v", in)
	}
}
