package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

// Regression test for the rate bug: byte deltas must be divided by the real
// elapsed time between samples (the slow interval), not an assumed 1s.
func TestIORatesUsesElapsedTime(t *testing.T) {
	prev := map[string]disk.IOCountersStat{
		"mmcblk0": {ReadBytes: 1000, WriteBytes: 2000},
	}
	cur := map[string]disk.IOCountersStat{
		"mmcblk0": {ReadBytes: 6000, WriteBytes: 12000},
	}

	ios := ioRates(prev, cur, 5.0)
	if len(ios) != 1 {
		t.Fatalf("got %d entries", len(ios))
	}
	if ios[0].ReadBps != 1000 {
		t.Errorf("ReadBps = %v, want 1000 (5000 bytes over 5s)", ios[0].ReadBps)
	}
	if ios[0].WriteBps != 2000 {
		t.Errorf("WriteBps = %v, want 2000 (10000 bytes over 5s)", ios[0].WriteBps)
	}
}

func TestIORatesFirstSampleAndOrder(t *testing.T) {
	cur := map[string]disk.IOCountersStat{
		"sdb": {ReadBytes: 100},
		"sda": {ReadBytes: 100},
	}
	ios := ioRates(nil, cur, 5.0)
	if len(ios) != 2 {
		t.Fatalf("got %d entries", len(ios))
	}
	if ios[0].Device != "sda" || ios[1].Device != "sdb" {
		t.Errorf("not sorted by device: %v %v", ios[0].Device, ios[1].Device)
	}
	if ios[0].ReadBps != 0 {
		t.Errorf("first sample should report 0 rate, got %v", ios[0].ReadBps)
	}

	// Non-positive dt must not divide by zero.
	ios = ioRates(cur, cur, 0)
	if ios[0].ReadBps != 0 {
		t.Errorf("dt=0: ReadBps = %v", ios[0].ReadBps)
	}
}

// An mmcblk/sd counter that goes backwards (driver reload, module reload,
// 32-bit wrap) must report 0, not the ~3.2 EiB/s unsigned wrap-around gives.
func TestIORatesCounterReset(t *testing.T) {
	prev := map[string]disk.IOCountersStat{
		"mmcblk0": {Name: "mmcblk0", ReadBytes: 5_000_000_000, WriteBytes: 4_000_000_000},
	}
	cur := map[string]disk.IOCountersStat{
		"mmcblk0": {Name: "mmcblk0", ReadBytes: 1_000_000, WriteBytes: 500_000},
	}
	got := ioRates(prev, cur, 5)
	if len(got) != 1 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].ReadBps != 0 || got[0].WriteBps != 0 {
		t.Errorf("counter reset gave read=%s write=%s, want 0",
			HumanBytesPerSec(got[0].ReadBps), HumanBytesPerSec(got[0].WriteBps))
	}
}

func withMountFixtures(t *testing.T, fstab, mounts string) {
	t.Helper()
	dir := t.TempDir()
	f, m := filepath.Join(dir, "fstab"), filepath.Join(dir, "mounts")
	if err := os.WriteFile(f, []byte(fstab), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m, []byte(mounts), 0o644); err != nil {
		t.Fatal(err)
	}
	oldF, oldM := fstabPath, mountsPath
	fstabPath, mountsPath = f, m
	t.Cleanup(func() { fstabPath, mountsPath = oldF, oldM })
}

func TestFstabAndLiveReadOnly(t *testing.T) {
	withMountFixtures(t, `# comment
PARTUUID=1-02  /               ext4  defaults,noatime  0 1
PARTUUID=1-01  /boot/firmware  vfat  defaults,ro       0 2
/dev/sda1      /mnt/my\040disk ext4  rw                0 0
/swapfile      none            swap  sw                0 0
/img.sqfs      /opt/app        squashfs defaults       0 0
`, `/dev/mmcblk0p2 / ext4 ro,noatime 0 0
/dev/mmcblk0p1 /boot/firmware vfat ro,relatime 0 0
/dev/sda1 /mnt/my\040disk ext4 rw,relatime 0 0
`)
	fs := fstabRW()
	want := map[string]bool{"/": true, "/boot/firmware": false, "/mnt/my disk": true}
	if len(fs) != len(want) {
		t.Errorf("fstab = %v, want %v", fs, want)
	}
	for k, v := range want {
		if got, ok := fs[k]; !ok || got != v {
			t.Errorf("fstab[%q] = %v,%v want %v", k, got, ok, v)
		}
	}

	live, ok := liveReadOnly()
	if !ok || !live["/"] || !live["/boot/firmware"] || live["/mnt/my disk"] {
		t.Errorf("live = %v, %v", live, ok)
	}
}

func TestMarkRemounts(t *testing.T) {
	fstab := map[string]bool{"/": true, "/boot/firmware": false}
	cases := []struct {
		name       string
		d          DiskInfo
		ro         bool
		sawRW      map[string]string
		wantRemont bool
	}{
		{"fstab rw, now ro", DiskInfo{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4"}, true, nil, true},
		{"fstab ro by design", DiskInfo{Device: "/dev/mmcblk0p1", MountPoint: "/boot/firmware", FSType: "vfat"}, true, nil, false},
		{"not in fstab, never seen rw", DiskInfo{Device: "/dev/sdb1", MountPoint: "/media/usb", FSType: "ext4"}, true, nil, false},
		{"not in fstab, seen rw earlier", DiskInfo{Device: "/dev/sdb1", MountPoint: "/media/usb", FSType: "ext4"}, true, map[string]string{"/media/usb": "/dev/sdb1"}, true},
		{"a different device at the same path", DiskInfo{Device: "/dev/sdc1", MountPoint: "/media/usb", FSType: "ext4"}, true, map[string]string{"/media/usb": "/dev/sdb1"}, false},
		{"squashfs is always ro", DiskInfo{Device: "/dev/loop0", MountPoint: "/", FSType: "squashfs"}, true, nil, false},
		{"rw is fine", DiskInfo{Device: "/dev/mmcblk0p2", MountPoint: "/", FSType: "ext4"}, false, nil, false},
	}
	for _, c := range cases {
		s := NewDiskSampler()
		for k, v := range c.sawRW {
			s.sawRW[k] = v
		}
		d := c.d
		s.markRemounts(&d, c.ro, fstab)
		if d.ReadOnly != c.ro || d.RemountedRO != c.wantRemont {
			t.Errorf("%s: ReadOnly=%v RemountedRO=%v, want %v/%v", c.name, d.ReadOnly, d.RemountedRO, c.ro, c.wantRemont)
		}
	}

	// Seeing a mount rw records it, so a later ro sample is a remount even
	// without an fstab entry — and a remount back to rw clears it.
	s := NewDiskSampler()
	sample := func(ro bool) DiskInfo { // a fresh DiskInfo per sample, as Sample builds it
		d := DiskInfo{Device: "/dev/sdb1", MountPoint: "/data", FSType: "ext4"}
		s.markRemounts(&d, ro, nil)
		return d
	}
	sample(false)
	if !sample(true).RemountedRO {
		t.Error("rw then ro was not flagged")
	}
	if d := sample(false); d.RemountedRO || d.ReadOnly {
		t.Error("remount back to rw did not clear the flag")
	}
}
