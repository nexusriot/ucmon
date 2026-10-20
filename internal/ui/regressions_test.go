package ui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nexusriot/ucmon/internal/config"
	"github.com/nexusriot/ucmon/internal/probe"
)

// Active conditions live in a map, so the banner used to pick an arbitrary one
// of several equally severe alerts — a host with four hot sensors cycled the
// footer between their messages on every frame.
func TestAlertBannerIsStableAcrossFrames(t *testing.T) {
	m := sizedModel(t, config.Default(), 120, 40)
	m.alerts.update(map[string]alertCond{
		"temp:d": {sevWarn, "sensor_d 88°C ≥ 87°C hot"},
		"temp:b": {sevWarn, "sensor_b 88°C ≥ 87°C hot"},
		"temp:a": {sevWarn, "sensor_a 88°C ≥ 87°C hot"},
		"temp:c": {sevWarn, "sensor_c 88°C ≥ 87°C hot"},
	}, time.Now())

	want := m.alertBanner()
	if want == "" {
		t.Fatal("no banner for four active alerts")
	}
	for i := 0; i < 200; i++ {
		if got := m.alertBanner(); got != want {
			t.Fatalf("banner changed between frames:\n  %q\n  %q", want, got)
		}
	}

	// It must also name whatever the Alerts tab lists first, or the banner and
	// the tab disagree about which condition is the headline.
	top, _, _ := m.alerts.summary()
	if first := m.alerts.activeList()[0]; first.msg != top.msg {
		t.Errorf("banner names %q, Alerts tab lists %q first", top.msg, first.msg)
	}
}

// Severity still decides; the message is only the tie-break.
func TestAlertBannerPrefersSeverityOverOrder(t *testing.T) {
	e := newAlertEngine(50, 1)
	e.update(map[string]alertCond{
		"a": {sevWarn, "aaa warning"}, // sorts first alphabetically
		"b": {sevCrit, "zzz critical"},
	}, time.Now())

	top, n, any := e.summary()
	if !any || n != 2 {
		t.Fatalf("summary = %v, %d, %v", top, n, any)
	}
	if top.sev != sevCrit {
		t.Errorf("banner picked %q (%s), want the critical", top.msg, top.sev.label())
	}
}

// /proc/diskstats carries a row per partition as well as per disk. Stripping
// the partition suffix up front gave every partition of a device the
// whole-disk rate, and mangled the names that end in a device index: a root
// filesystem on LVM (/dev/dm-0) reported no disk I/O at all.
func TestDiskIOForPrefersTheExactDevice(t *testing.T) {
	ios := map[string]probe.DiskIOInfo{
		"nvme0n1":   {Device: "nvme0n1", ReadBps: 1000, WriteBps: 1000},
		"nvme0n1p1": {Device: "nvme0n1p1", ReadBps: 1, WriteBps: 1},
		"nvme0n1p3": {Device: "nvme0n1p3", ReadBps: 3, WriteBps: 3},
		"dm-0":      {Device: "dm-0", ReadBps: 7, WriteBps: 7},
		"mmcblk0":   {Device: "mmcblk0", ReadBps: 9, WriteBps: 9},
		"sda":       {Device: "sda", ReadBps: 5, WriteBps: 5},
	}
	cases := []struct {
		dev     string
		wantBps float64
		wantOK  bool
	}{
		{"/dev/nvme0n1p1", 1, true},  // its own row, not the disk's 1000
		{"/dev/nvme0n1p3", 3, true},  // ditto — these two used to be equal
		{"/dev/nvme0n1", 1000, true}, // whole namespace mounted directly
		{"/dev/dm-0", 7, true},       // LVM / dm-crypt root: used to miss
		{"/dev/mmcblk0", 9, true},    // whole card: used to become "mmcblk"
		{"/dev/sda", 5, true},        // whole disk
		{"/dev/sda1", 5, true},       // no partition row: falls back to sda
		{"/dev/mapper/vg-root", 0, false},
		{"tmpfs", 0, false},
	}
	for _, c := range cases {
		io, ok := diskIOFor(ios, c.dev)
		if ok != c.wantOK || io.ReadBps != c.wantBps {
			t.Errorf("diskIOFor(%q) = %v, %v; want %v, %v",
				c.dev, io.ReadBps, ok, c.wantBps, c.wantOK)
		}
	}
}

func TestBaseDeviceKeepsWholeDeviceNames(t *testing.T) {
	cases := [][2]string{
		{"/dev/mmcblk0p2", "mmcblk0"},
		{"/dev/nvme0n1p3", "nvme0n1"},
		{"/dev/nvme0n1p12", "nvme0n1"},
		{"/dev/sda1", "sda"},
		{"/dev/sda", "sda"},
		{"/dev/dm-0", "dm-0"}, // used to come back as "dm-"
		{"/dev/root", "root"},
	}
	for _, c := range cases {
		if got := baseDevice(c[0]); got != c[1] {
			t.Errorf("baseDevice(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

// End to end through the JSON snapshot: two partitions of one disk must not
// report the same rate.
func TestSnapshotJSONGivesPartitionsTheirOwnIO(t *testing.T) {
	m := NewModel(config.Default())
	m.disks = []probe.DiskInfo{
		{Device: "/dev/nvme0n1p1", MountPoint: "/boot/efi", Total: 100, Used: 10},
		{Device: "/dev/nvme0n1p3", MountPoint: "/boot", Total: 100, Used: 10},
		{Device: "/dev/dm-0", MountPoint: "/", Total: 100, Used: 50, Free: 50},
	}
	m.diskIOs = []probe.DiskIOInfo{
		{Device: "nvme0n1", ReadBps: 9999},
		{Device: "nvme0n1p1", ReadBps: 11},
		{Device: "nvme0n1p3", ReadBps: 33},
		{Device: "dm-0", ReadBps: 77},
	}

	body, err := m.buildJSONReport(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Disks []struct {
			Mount   string  `json:"mount"`
			ReadBps float64 `json:"read_bps"`
		} `json:"disks"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"/boot/efi": 11, "/boot": 33, "/": 77}
	for _, d := range doc.Disks {
		if w, ok := want[d.Mount]; ok && d.ReadBps != w {
			t.Errorf("%s read_bps = %v, want %v", d.Mount, d.ReadBps, w)
		}
		delete(want, d.Mount)
	}
	if len(want) != 0 {
		t.Errorf("mounts missing from the snapshot: %v", want)
	}
}

// filesystem_free_bytes has to agree with the Disk tab, which shows Free
// (blocks available to this user) rather than Total-Used (which includes the
// root-reserved blocks).
func TestPrometheusFreeBytesMatchesTheDiskTab(t *testing.T) {
	m := NewModel(config.Default())
	m.disks = []probe.DiskInfo{
		{Device: "/dev/sda1", MountPoint: "/", FSType: "ext4",
			Total: 1000, Used: 900, Free: 50, UsedPct: 90},
	}
	out := m.buildPrometheus()
	want := `ucmon_filesystem_free_bytes{mount="/",fstype="ext4"} 50`
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
}

// The connection rows carry the same column widths as the header, so a resize
// has to rebuild both; rebuilding only the header left the table misaligned
// until the next socket walk.
func TestResizeRerendersTheConnectionTable(t *testing.T) {
	m := sizedModel(t, config.Default(), 60, 30)
	m.activeTab = tabNet
	m.conns = []probe.ConnInfo{{
		Proto: "TCP", LocalAddr: "10.0.0.1:22", RemoteAddr: "10.0.0.99:51000",
		Status: "ESTABLISHED", PID: 1, Process: "sshd",
	}}
	next, _ := m.Update(connMsg(m.conns))
	m = next.(Model)
	narrow := m.netText

	next, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = next.(Model)

	if m.netText == narrow {
		t.Fatal("connection rows were not re-rendered for the new width")
	}
	// Header and rows must agree on the local-address column width.
	_, colProto, colLocal, _, _, _ := m.netCols()
	row := strings.SplitN(m.netText, "\n", 2)[0]
	field := row[colProto+2:]
	if len(field) < colLocal {
		t.Fatalf("row is narrower than the header's columns: %q", row)
	}
	if !strings.HasPrefix(strings.TrimSpace(field), "10.0.0.1:22") {
		t.Errorf("local address not where the header puts it: %q", row)
	}
}

// metrics_listen runs the exporter beside the TUI: the exporter builds a fresh
// Model per scrape (which applies the process-wide temperature thresholds)
// while the monitor's own model keeps sampling and rendering. Run under -race.
func TestExporterAndModelRunConcurrently(t *testing.T) {
	cfg := config.Default()
	cfg.PingEnabled = false

	srv := httptest.NewServer(NewExporter(cfg).Handler())
	defer srv.Close()

	m := sizedModel(t, cfg, 100, 40)
	done := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			resp, err := http.Get(srv.URL + "/metrics")
			if err != nil {
				t.Errorf("scrape: %v", err)
				break
			}
			resp.Body.Close()
		}
		close(done)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			next, _ := m.Update(fetchCPUCmd(m.cpuSampler)())
			mm := next.(Model)
			_ = mm.View()
		}
	}()
	wg.Wait()
}

// Two samplers must not consume each other's baseline: with metrics_listen
// set, every scrape used to re-prime the shared gopsutil baseline the TUI was
// about to diff against, so the next frame measured the exporter's 600 ms
// window — or reported 0% outright when the two landed close together.
func TestCPUSamplersDoNotShareABaseline(t *testing.T) {
	tui := sizedModel(t, config.Default(), 100, 40)
	exporter := probe.NewCPUSampler()

	// Prime the TUI's sampler, then let the "exporter" sample repeatedly.
	if _, err := tui.cpuSampler.Sample(); err != nil {
		t.Fatalf("prime: %v", err)
	}
	for i := 0; i < 4; i++ {
		if _, err := exporter.Sample(); err != nil {
			t.Fatalf("exporter sample: %v", err)
		}
	}

	snap, err := tui.cpuSampler.Sample()
	if err != nil {
		t.Fatalf("tui sample: %v", err)
	}
	if !snap.Breakdown.Valid {
		t.Error("the TUI's sampler lost its baseline to the exporter's")
	}
}
