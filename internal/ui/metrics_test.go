package ui

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

func metricsModel() Model {
	m := NewModel(config.Default())
	m.netSnap = probe.NetSnapshot{
		Hostname: "pi", Uptime: 90 * time.Minute,
		Ifaces: []probe.IfaceInfo{{
			Name: "wlan0", IsUp: true, Addrs: []string{"192.168.1.5/24"},
			RxBps: 1024, TxBps: 2048,
			ErrIn: 3, DropIn: 7, HasCounter: true,
			WiFi: probe.WiFiInfo{Present: true, QualityPct: 80, SignalDBm: -52},
		}},
	}
	m.cpuSnap = probe.CPUSnapshot{
		TotalPercent: 42,
		Temperatures: []probe.TempReading{{Label: "cpu_thermal", Temp: 61.5}},
		Breakdown: probe.CPUBreakdown{
			Valid: true, User: 20, System: 10, Iowait: 12, Idle: 58, Busiest: "user",
		},
	}
	m.memSnap = probe.MemSnapshot{Total: 4 << 30, Used: 2 << 30, UsedPct: 50}
	m.powerSnap = probe.PowerSnapshot{
		Load:    probe.LoadInfo{Load1: 1.5, Load5: 1.2, Load15: 0.9, CPUCount: 4},
		Battery: probe.BatteryInfo{Present: true, Capacity: 77, Status: "Discharging", PowerW: 3.2},
	}
	m.disks = []probe.DiskInfo{{MountPoint: "/", Device: "/dev/mmcblk0p2", FSType: "ext4",
		Total: 32 << 30, Used: 16 << 30, UsedPct: 50}}
	m.storage = []probe.StorageHealth{{Device: "mmcblk0", Kind: "eMMC", LifeA: 4, PreEOL: 1}}
	m.pings = []probe.PingResult{{Target: "gateway", Addr: "192.168.1.1", OK: true,
		RTT: 3 * time.Millisecond, Method: "icmp"}}
	m.lossWin = map[string][]bool{"gateway": {true, true, false, true}}
	m.procs = []probe.ProcInfo{
		{PID: 1, Name: "systemd", User: "root", CPUPct: 1.5, MemPct: 0.4, MemRSS: 1 << 20},
		{PID: 42, Name: "ucmon", User: "pi", CPUPct: 9, MemPct: 1,
			IOReadBps: 100, IOWriteBps: 200, IOKnown: true},
	}
	return m
}

func TestPrometheusExposition(t *testing.T) {
	out := metricsModel().buildPrometheus()

	for _, want := range []string{
		"# TYPE ucmon_cpu_usage_percent gauge",
		"ucmon_cpu_usage_percent 42",
		`ucmon_cpu_mode_percent{mode="iowait"} 12`,
		`ucmon_temperature_celsius{sensor="cpu_thermal"} 61.5`,
		"ucmon_memory_used_percent 50",
		"ucmon_load1 1.5",
		`ucmon_filesystem_used_percent{mount="/",fstype="ext4"} 50`,
		`ucmon_network_receive_bytes_per_second{device="wlan0"} 1024`,
		`ucmon_network_bad_frames_total{device="wlan0"} 10`,
		`ucmon_wifi_signal_dbm{device="wlan0"} -52`,
		"ucmon_battery_percent 77",
		`ucmon_storage_wear_percent{device="mmcblk0",kind="eMMC"} 40`,
		`ucmon_ping_rtt_milliseconds{target="gateway"} 3`,
		`ucmon_ping_loss_percent{target="gateway"} 25`,
		"ucmon_alerts_active 0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("exposition missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// Every metric line must be preceded by its HELP and TYPE, or a scrape
// rejects the whole document.
func TestPrometheusWellFormed(t *testing.T) {
	out := metricsModel().buildPrometheus()

	declared := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "# TYPE "):
			declared[strings.Fields(line)[2]] = true
		case strings.HasPrefix(line, "# HELP "):
		default:
			name, _, _ := strings.Cut(line, "{")
			name, _, _ = strings.Cut(name, " ")
			if !declared[name] {
				t.Errorf("sample %q has no preceding # TYPE", line)
			}
			if fields := strings.Fields(line); len(fields) < 2 {
				t.Errorf("sample %q has no value", line)
			}
		}
	}
}

func TestPrometheusEmptyModelIsStillValid(t *testing.T) {
	out := NewModel(config.Default()).buildPrometheus()
	if !strings.Contains(out, "ucmon_cpu_usage_percent") {
		t.Error("an unsampled model produced no metrics at all")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "NaN") || strings.Contains(line, "+Inf") {
			t.Errorf("unsampled model emitted %q", line)
		}
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	body, err := metricsModel().buildJSONReport(time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var got SnapshotJSON
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if got.Version != version {
		t.Errorf("version = %q", got.Version)
	}
	if got.CPU.TotalPct != 42 || got.CPU.Busiest != "user" {
		t.Errorf("cpu = %+v", got.CPU)
	}
	if got.CPU.Breakdown["iowait"] != 12 {
		t.Errorf("breakdown = %v", got.CPU.Breakdown)
	}
	if got.Battery == nil || got.Battery.Pct != 77 {
		t.Errorf("battery = %+v", got.Battery)
	}
	if len(got.Storage) != 1 || got.Storage[0].WearPct != 40 {
		t.Errorf("storage = %+v", got.Storage)
	}
	if len(got.Network) != 1 || got.Network[0].DropIn != 7 {
		t.Errorf("network = %+v", got.Network)
	}
	if len(got.TopProcs) != 2 || got.TopProcs[0].Name != "ucmon" {
		t.Errorf("top procs not sorted by CPU: %+v", got.TopProcs)
	}
	if got.TopProcs[0].IOWriteBps != 200 {
		t.Errorf("per-process I/O missing from JSON: %+v", got.TopProcs[0])
	}
	if !strings.HasSuffix(body, "\n") {
		t.Error("JSON output has no trailing newline")
	}
}

func TestExporterServesBothFormats(t *testing.T) {
	e := NewExporter(config.Default())
	srv := httptest.NewServer(e.Handler())
	defer srv.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, body := get("/metrics"); code != 200 || !strings.Contains(body, "ucmon_cpu_usage_percent") {
		t.Errorf("/metrics = %d, %q", code, truncForTest(body))
	}
	if code, body := get("/snapshot"); code != 200 {
		t.Errorf("/snapshot = %d, %q", code, truncForTest(body))
	} else if !json.Valid([]byte(body)) {
		t.Error("/snapshot did not return valid JSON")
	}
	if code, body := get("/healthz"); code != 200 || !strings.Contains(body, "ok") {
		t.Errorf("/healthz = %d, %q", code, body)
	}
	if code, _ := get("/"); code != 200 {
		t.Errorf("index = %d", code)
	}
	if code, _ := get("/nope"); code != 404 {
		t.Errorf("unknown path = %d, want 404", code)
	}
}

// A careless 1s scrape must not drive a full /proc walk as fast as it can
// ask; the exporter reuses a recent sample instead.
func TestExporterCachesBetweenScrapes(t *testing.T) {
	e := NewExporter(config.Default())

	first, err := e.snapshot()
	if err != nil {
		t.Fatalf("first scrape: %v", err)
	}
	second, err := e.snapshot()
	if err != nil {
		t.Fatalf("second scrape: %v", err)
	}
	if first != second {
		t.Error("two scrapes inside the minimum interval re-sampled the system")
	}
}

func TestServeStopsOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a port here: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, config.Default(), addr) }()

	// Wait for the listener to come up.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.Dial("tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v after cancel, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after its context was cancelled")
	}
}

// A port already in use must be reported, not discovered later from an empty
// graph.
func TestServeReportsBindFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a port here: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := Serve(ctx, config.Default(), ln.Addr().String()); err == nil {
		t.Error("Serve on an occupied port returned nil")
	}
}

func TestListenAddr(t *testing.T) {
	cases := [][2]string{
		{"", ""},
		{"9100", ":9100"},
		{":9100", ":9100"},
		{"127.0.0.1:9100", "127.0.0.1:9100"},
	}
	for _, c := range cases {
		if got := ListenAddr(c[0]); got != c[1] {
			t.Errorf("ListenAddr(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestBaseDevice(t *testing.T) {
	cases := [][2]string{
		{"/dev/mmcblk0p2", "mmcblk0"},
		{"/dev/sda1", "sda"},
		{"/dev/nvme0n1p3", "nvme0n1"},
		{"/dev/sda", "sda"},
	}
	for _, c := range cases {
		if got := baseDevice(c[0]); got != c[1] {
			t.Errorf("baseDevice(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func truncForTest(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
