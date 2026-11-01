package ui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

	first, _, err := e.snapshot(context.Background())
	if err != nil {
		t.Fatalf("first scrape: %v", err)
	}
	second, _, err := e.snapshot(context.Background())
	if err != nil {
		t.Fatalf("second scrape: %v", err)
	}
	if first != second {
		t.Error("two scrapes inside the minimum interval re-sampled the system")
	}
}

// A collection stuck in the kernel must not take the exporter with it:
// scrapes keep being answered from the last good snapshot, at most one
// collection runs, and a scrape that has no snapshot to fall back on fails
// fast instead of hanging.
func TestExporterSurvivesAHungCollection(t *testing.T) {
	e := NewExporter(config.Default())
	e.wait = 100 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	e.collect = func(cfg config.Config) Model {
		if calls.Add(1) > 1 {
			<-release // the second collection hangs
		}
		m := testModel()
		m.memSnap.UsedPct = float64(calls.Load())
		return m
	}

	first, at, err := e.snapshot(context.Background())
	if err != nil || first.memSnap.UsedPct != 1 {
		t.Fatalf("first scrape: %v", err)
	}
	e.mu.Lock()
	e.lastAt = at.Add(-time.Hour) // expire it
	e.mu.Unlock()

	for range 3 {
		start := time.Now()
		m, _, err := e.snapshot(context.Background())
		if err != nil || m != first {
			t.Fatalf("scrape during a hung collection: %v (fresh model %v)", err, m != first)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Fatalf("a scrape waited %v on the hung collection", d)
		}
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("%d collections started, want the one hung one", n)
	}

	// A fresh exporter whose first collection hangs answers with an error.
	e2 := NewExporter(config.Default())
	e2.wait = 100 * time.Millisecond
	e2.collect = func(cfg config.Config) Model { <-release; return testModel() }
	if _, _, err := e2.snapshot(context.Background()); !errors.Is(err, errNoSnapshot) {
		t.Errorf("no snapshot yet: err = %v", err)
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

// Label values follow the exposition format's escaping, not Go's: a tab, a
// control byte or invalid UTF-8 in one mount path or sensor label must not
// make Prometheus reject the whole scrape.
func TestPromLabelEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"plain":         `"plain"`,
		`back\slash`:    `"back\\slash"`,
		`qu"ote`:        `"qu\"ote"`,
		"new\nline":     `"new\nline"`,
		"tab\there":     "\"tab\there\"",
		"ctl\x01":       "\"ctl\x01\"",
		"bad\xffutf8":   "\"bad\uFFFDutf8\"",
		"soft\u00adhyp": "\"soft\u00adhyp\"",
	} {
		if got := promLabel(in); got != want {
			t.Errorf("promLabel(%q) = %q, want %q", in, got, want)
		}
	}

	m := testModel()
	m.disks = []probe.DiskInfo{{Device: "/dev/sda1", MountPoint: "/mnt/odd\tname\xff", FSType: "ext4", Total: 100, Used: 10, UsedPct: 10}}
	out := m.buildPrometheus()
	if !strings.Contains(out, "mount=\"/mnt/odd\tname\uFFFD\"") || strings.Contains(out, `\t`) || strings.Contains(out, `\x`) {
		t.Errorf("an odd mount path was escaped Go-style:\n%s", out)
	}
}
