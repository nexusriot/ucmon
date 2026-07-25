package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func TestBuildReportSections(t *testing.T) {
	m := NewModel(config.Default())

	m.cpuSnap = probe.CPUSnapshot{
		TotalPercent:   42.5,
		PerCorePercent: []float64{40, 45},
		Temperatures:   []probe.TempReading{{Label: "cpu_thermal", Temp: 61.2}},
		Freqs: []probe.PolicyFreq{{
			Policy: "policy0", CurMHz: 1500, MinMHz: 600, MaxMHz: 1800, Governor: "ondemand",
		}},
		TakenAt: time.Now(),
	}
	m.memSnap = probe.MemSnapshot{Total: 4 << 30, Used: 2 << 30, UsedPct: 50, TakenAt: time.Now()}
	m.powerSnap = probe.PowerSnapshot{
		Load: probe.LoadInfo{Load1: 1.2, Load5: 1.0, Load15: 0.8, CPUCount: 4},
		Battery: probe.BatteryInfo{
			Present: true, Capacity: 77, Status: "Discharging",
			PowerW: 2.5, HealthPct: 85,
		},
	}
	m.psi = probe.PSISnapshot{
		Present: true,
		CPU:     probe.PSIRes{HasSome: true, SomeAvg10: 3.5, SomeAvg60: 1.2},
		Mem:     probe.PSIRes{HasSome: true, HasFull: true, FullAvg10: 0.4},
	}
	m.disks = []probe.DiskInfo{{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4",
		Total: 32 << 30, Used: 20 << 30, UsedPct: 62.5}}
	m.netSnap = probe.NetSnapshot{
		TakenAt: time.Now(),
		Ifaces: []probe.IfaceInfo{{
			Name: "wlan0", IsUp: true, RxBps: 1024, TxBps: 512,
			WiFi: probe.WiFiInfo{Present: true, QualityPct: 80, SignalDBm: -54},
		}},
	}
	m.iwLinks = map[string]probe.IWLink{
		"wlan0": {Present: true, SSID: "NightCity", Channel: 36, FreqMHz: 5180},
	}
	m.pings = []probe.PingResult{
		{Target: "gateway", Addr: "192.168.1.1", OK: true, RTT: 3 * time.Millisecond, Method: "icmp"},
	}
	m.lossWin["gateway"] = []bool{true, true, false, true}
	m.procs = []probe.ProcInfo{{PID: 1, Name: "systemd", CPUPct: 0.5}}
	m.peaks.observeCPU(88.8, m.cpuSnap.Temperatures)
	m.peaks.observeMem(75)
	m.peaks.observeLoad(3.2)
	m.peaks.observeBattery(42)

	report := m.buildReport(time.Now())

	for _, want := range []string{
		"CPU",
		"Frequency",
		"policy0",
		"governor ondemand",
		"Pressure (PSI",
		"Memory",
		"Power",
		"health 85%",
		"Disks",
		"Network",
		"ssid \"NightCity\"",
		"Latency",
		"gateway (192.168.1.1)",
		"loss 25%/4",
		"Top processes",
		"Session peaks",
		"battery min    42%",
		"Alerts",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q\n---\n%s", want, report)
		}
	}

	if strings.Contains(report, "\x1b[") {
		t.Error("report contains ANSI escapes")
	}
}

func TestExportSnapshotWritesToHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := NewModel(config.Default())
	path, err := m.exportSnapshot()
	if err != nil {
		t.Fatalf("exportSnapshot: %v", err)
	}
	if !strings.HasPrefix(path, home) {
		t.Errorf("report path %q not under HOME %q", path, home)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "ucmon "+version) {
		t.Errorf("report header = %q", string(b[:40]))
	}
}

func TestThrottleFlags(t *testing.T) {
	if got := throttleFlags(probe.ThrottleInfo{}); got != " (ok)" {
		t.Errorf("clean flags = %q", got)
	}
	got := throttleFlags(probe.ThrottleInfo{UnderVoltageNow: true, ThrottledNow: true})
	if !strings.Contains(got, "under-voltage") || !strings.Contains(got, "throttled") {
		t.Errorf("flags = %q", got)
	}
}

func TestTruncNameAndBoolWord(t *testing.T) {
	if got := truncName("short", 10); got != "short" {
		t.Errorf("truncName = %q", got)
	}
	if got := truncName("a-very-long-process-name", 10); got != "a-very-lo…" {
		t.Errorf("truncName = %q", got)
	}
	if boolWord(true, "on", "off") != "on" || boolWord(false, "on", "off") != "off" {
		t.Error("boolWord")
	}
}
