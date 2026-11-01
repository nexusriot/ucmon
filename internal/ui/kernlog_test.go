package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

func kernelConds(m Model) map[string]alertCond {
	out := map[string]alertCond{}
	for k, c := range m.currentAlertConds() {
		if strings.HasPrefix(k, "kio:") || k == "kuv" {
			out[k] = c
		}
	}
	return out
}

func kmsgAt(t *testing.T, m Model, evs ...probe.KernelEvent) Model {
	t.Helper()
	next, _ := m.Update(kmsgMsg{events: evs})
	return next.(Model)
}

func ioErr(disk, part string, at time.Time) probe.KernelEvent {
	return probe.KernelEvent{Kind: probe.KernelStorageError, Device: disk, Partition: part, At: at}
}

func TestKernelLogAlerts(t *testing.T) {
	now := time.Now()
	m := testModel()
	m.cfg.EventAlertMinutes = 10
	m.now = now
	m.disks = []probe.DiskInfo{
		{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4"},
		{Device: "/dev/mmcblk0p1", MountPoint: "/boot/firmware", FSType: "vfat"},
	}

	// One card logging under three names (host, disk, partition) is one alert.
	m = kmsgAt(t, m, ioErr("mmcblk0", "mmcblk0p2", now), ioErr("mmcblk0", "mmcblk0p2", now),
		ioErr("mmcblk0", "mmc0", now), ioErr("mmcblk0", "mmcblk0", now),
		probe.KernelEvent{Kind: probe.KernelUnderVoltage, At: now})

	c := kernelConds(m)
	if len(c) != 2 {
		t.Fatalf("conditions: %+v, want one storage alert and one under-voltage", c)
	}
	if g := c["kio:mmcblk0"]; g.sev != sevCrit || !strings.Contains(g.msg, "mmcblk0 (/, /boot/firmware)") || !strings.Contains(g.msg, "4×") {
		t.Errorf("storage alert: %+v", g)
	}
	if g := c["kuv"]; g.sev != sevWarn || !strings.Contains(g.msg, "under-voltage") {
		t.Errorf("under-voltage: %+v", g)
	}

	m.activeTab = tabDisk
	if !strings.Contains(m.viewDisk(), "kernel I/O errors this session: mmcblk0 4") {
		t.Error("Disk tab lacks the kernel I/O error tally")
	}
	m.activeTab = tabPower
	m.powerSnap = probe.PowerSnapshot{TakenAt: now}
	if !strings.Contains(m.viewPower(), "under-voltage 1× this session") {
		t.Error("Power tab lacks the kernel under-voltage tally")
	}

	// Past the window the alerts go, but the session tally stays.
	m.now = now.Add(11 * time.Minute)
	if c := kernelConds(m); len(c) != 0 {
		t.Errorf("after the window: %+v", c)
	}
	m.cfg.EventAlertMinutes = 0
	m.now = now
	if c := kernelConds(m); len(c) != 0 {
		t.Errorf("event_alert_minutes = 0 still raised %+v", c)
	}
}

// A disk whose every mount disk_ignore hides has its kernel errors ignored
// too; a disk with one visible mount does not.
func TestKernelLogHonoursDiskIgnore(t *testing.T) {
	now := time.Now()
	m := testModel()
	m.cfg.DiskIgnore = []string{"/media/*"}
	m.now = now
	next, _ := m.Update(diskMsg{disks: []probe.DiskInfo{
		{Device: "/dev/sdb1", MountPoint: "/media/usb", FSType: "vfat"},
		{Device: "/dev/sda1", MountPoint: "/media/a", FSType: "ext4"},
		{Device: "/dev/sda2", MountPoint: "/data", FSType: "ext4"},
	}})
	m = next.(Model)
	m = kmsgAt(t, m, ioErr("sdb", "sdb1", now), ioErr("sda", "sda1", now))
	c := kernelConds(m)
	if _, ok := c["kio:sdb"]; ok {
		t.Error("an ignored disk raised a kernel I/O alert")
	}
	if _, ok := c["kio:sda"]; !ok {
		t.Error("a disk with a visible mount lost its alert")
	}
}

func TestMountOfMatchesWholeDisksOnly(t *testing.T) {
	m := testModel()
	m.disks = []probe.DiskInfo{
		{Device: "/dev/loop10", MountPoint: "/snap/a"},
		{Device: "/dev/sdb12", MountPoint: "/b12"},
		{Device: "/dev/nvme0n10p1", MountPoint: "/n10"},
		{Device: "/dev/nvme0n1p2", MountPoint: "/n1"},
		{Device: "/dev/sdb1", MountPoint: "/b1"},
	}
	for disk, want := range map[string]string{"sdb": "/b1, /b12", "nvme0n1": "/n1", "nvme0n10": "/n10", "loop1": "", "sdb1": ""} {
		if got := m.mountOf(disk); got != want {
			t.Errorf("mountOf(%q) = %q, want %q", disk, got, want)
		}
	}
}

// Events keep flowing while sampling is paused: a kill or an I/O error in a
// pause is still an alert, dated when it happened.
func TestEventStreamsRunWhilePaused(t *testing.T) {
	m := testModel()
	m.paused = true
	m.kmsg = openEmptyKmsg(t)
	defer func() { _ = m.kmsg.Close() }()
	m, _ = press(t, m, tickMsg(time.Now()))
	if !m.inFlight[fetchKmsg] {
		t.Error("the kernel log is not drained while paused")
	}
	if !m.inFlight[fetchMem] {
		t.Error("the OOM counter is not read while paused")
	}
	before := m.memSnap
	next, _ := m.Update(oomCountMsg{n: 3, ok: true, at: time.Now()})
	m = next.(Model)
	if m.memSnap != before || !m.oom.primed || m.oom.last != 3 {
		t.Errorf("the paused OOM read: memSnap changed %v, tracker %+v", m.memSnap != before, m.oom)
	}
	if m.inFlight[fetchMem] {
		t.Error("the OOM read did not lower its guard")
	}
}

// After kmsgMaxErrors failed reads in a row the log is dropped with one
// notice, instead of an error flickering in the footer forever; a read that
// fails still delivers what it parsed first.
func TestKernelLogReadErrors(t *testing.T) {
	now := time.Now()
	m := testModel()
	m.now = now
	m.kmsg = openEmptyKmsg(t)
	readErr := errors.New("read /dev/kmsg: invalid argument")
	next, _ := m.Update(kmsgMsg{events: []probe.KernelEvent{ioErr("sda", "sda", now)}, err: readErr})
	m = next.(Model)
	if _, ok := kernelConds(m)["kio:sda"]; !ok {
		t.Error("events parsed before the error were dropped")
	}
	for range kmsgMaxErrors - 1 {
		next, _ = m.Update(kmsgMsg{err: readErr})
		m = next.(Model)
	}
	if m.kmsg != nil {
		t.Fatal("the log is still followed after repeated failures")
	}
	if m.err != nil || !strings.Contains(m.notice.get(), "stopped following") {
		t.Errorf("err %v, notice %q", m.err, m.notice.get())
	}
}

// The counter says how many; the kernel log says who. Each victim belongs to
// the nearest kill, so two kills seconds apart are named apart.
func TestOOMAlertNamesTheVictim(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	m := testModel()
	m.cfg.EventAlertMinutes = 10
	m = memAt(t, m, 1, t0)
	m = kmsgAt(t, m, probe.KernelEvent{Kind: probe.KernelOOMKill, PID: 11, Comm: "alpha", At: t0.Add(time.Second)})
	m = memAt(t, m, 2, t0.Add(time.Second))
	m = kmsgAt(t, m, probe.KernelEvent{Kind: probe.KernelOOMKill, PID: 22, Comm: "beta", At: t0.Add(6 * time.Second)})
	m = memAt(t, m, 3, t0.Add(6*time.Second))

	c := oomConds(m)
	if !strings.Contains(c["oom:2"].msg, "killed alpha (pid 11) at") || strings.Contains(c["oom:2"].msg, "beta") {
		t.Errorf("first kill = %q", c["oom:2"].msg)
	}
	if !strings.Contains(c["oom:3"].msg, "killed beta (pid 22) at") || strings.Contains(c["oom:3"].msg, "alpha") {
		t.Errorf("second kill = %q", c["oom:3"].msg)
	}
	m.activeTab = tabMem
	if !strings.Contains(m.viewMem(), "beta (pid 22)") {
		t.Error("Memory tab does not name the last victim")
	}

	// Two killed, one named: the other is counted, not invented.
	m = kmsgAt(t, m, probe.KernelEvent{Kind: probe.KernelOOMKill, PID: 33, Comm: "gamma", At: t0.Add(time.Minute)})
	m = memAt(t, m, 5, t0.Add(time.Minute))
	if msg := oomConds(m)["oom:5"].msg; !strings.Contains(msg, "gamma (pid 33) and 1 more") {
		t.Errorf("partly named kill = %q", msg)
	}
}

// Without the vmstat counter the log line is the only sign of a kill, so it
// raises the alert itself.
func TestOOMFromTheLogAloneWithoutTheCounter(t *testing.T) {
	t0 := time.Now()
	m := testModel()
	m.cfg.EventAlertMinutes = 10
	m.now = t0
	m = kmsgAt(t, m, probe.KernelEvent{Kind: probe.KernelOOMKill, PID: 7, Comm: "java", At: t0})
	c := oomConds(m)
	if len(c) != 1 || !strings.Contains(c["oom:log1"].msg, "killed java (pid 7)") {
		t.Fatalf("no-counter OOM: %+v", c)
	}
}

// openEmptyKmsg follows an empty file standing in for the kernel log.
func openEmptyKmsg(t *testing.T) *probe.KmsgWatcher {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kmsg")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := probe.OpenKmsgAt(path)
	if err != nil {
		t.Fatal(err)
	}
	return w
}
