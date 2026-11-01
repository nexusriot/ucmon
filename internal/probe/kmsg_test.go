package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Lines as real kernels print them (Raspberry Pi, x86, cgroup OOM).
func TestClassifyKernelLine(t *testing.T) {
	for _, tc := range []struct {
		line string
		kind KernelEventKind
		dev  string // the whole disk
		part string // the name the line used, when it differs
		comm string
		pid  int
	}{
		{line: "hwmon hwmon1: Undervoltage detected!", kind: KernelUnderVoltage},
		{line: "Under-voltage detected! (0x00050005)", kind: KernelUnderVoltage},
		{line: "Out of memory: Killed process 1234 (chromium) total-vm:1693280kB, anon-rss:507428kB", kind: KernelOOMKill, comm: "chromium", pid: 1234},
		{line: "Memory cgroup out of memory: Killed process 4061915 (e2e.test) total-vm:1758888kB, UID:10001", kind: KernelOOMKill, comm: "e2e.test", pid: 4061915},
		{line: "I/O error, dev mmcblk0, sector 1234 op 0x1:(WRITE) flags 0x800 phys_seg 1 prio class 2", kind: KernelStorageError, dev: "mmcblk0"},
		{line: "blk_update_request: I/O error, dev sda, sector 2048 op 0x0:(READ)", kind: KernelStorageError, dev: "sda"},
		{line: "Buffer I/O error on dev mmcblk0p2, logical block 0, lost async page write", kind: KernelStorageError, dev: "mmcblk0", part: "mmcblk0p2"},
		{line: "mmcblk0: error -110 transferring data, sector 123, nr 8, cmd response 0x900", kind: KernelStorageError, dev: "mmcblk0"},
		{line: "mmc0: Timeout waiting for hardware interrupt.", kind: KernelStorageError, dev: "mmc0"},
		{line: "EXT4-fs error (device mmcblk0p2): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0", kind: KernelStorageError, dev: "mmcblk0", part: "mmcblk0p2"},
		{line: "EXT4-fs (sda1): I/O error while writing superblock", kind: KernelStorageError, dev: "sda", part: "sda1"},
		{line: "FAT-fs (mmcblk0p1): error, fat_get_cluster: invalid cluster chain", kind: KernelStorageError, dev: "mmcblk0", part: "mmcblk0p1"},
		// Pre-5.1 wording: a bare line after "Out of memory: Kill process …".
		{line: "Killed process 812 (java) total-vm:2034520kB, anon-rss:1023104kB", kind: KernelOOMKill, comm: "java", pid: 812},
		{line: "blk_update_request: critical medium error, dev sda, sector 4096 op 0x0:(READ)", kind: KernelStorageError, dev: "sda"},
		{line: "EXT4-fs warning (device sda1): ext4_end_bio:347: I/O error 10 writing to inode 12", kind: KernelStorageError, dev: "sda", part: "sda1"},
		{line: "F2FS-fs (mmcblk0p2): error: inconsistent node block", kind: KernelStorageError, dev: "mmcblk0", part: "mmcblk0p2"},
		{line: "BTRFS error (device nvme0n1p3): bdev /dev/nvme0n1p3 errs: wr 1, rd 0", kind: KernelStorageError, dev: "nvme0n1", part: "nvme0n1p3"},
	} {
		part := tc.part
		if part == "" {
			part = tc.dev
		}
		ev, ok := ClassifyKernelLine(tc.line)
		if !ok || ev.Kind != tc.kind || ev.Device != tc.dev || (tc.kind == KernelStorageError && ev.Partition != part) || ev.Comm != tc.comm || ev.PID != tc.pid {
			t.Errorf("%q → %+v, %v", tc.line, ev, ok)
		}
	}

	// Lines that look close but are not trouble.
	for _, line := range []string{
		"EXT4-fs (mmcblk0p2): re-mounted 1a2b. Quota mode: none. Opts: errors=remount-ro",
		"EXT4-fs (mmcblk0p2): mounted filesystem with ordered data mode",
		"Voltage normalised (0x00000000)",
		"hwmon hwmon1: Voltage normalised",
		"oom_reaper: reaped process 1234 (chromium), now anon-rss:0kB",
		"veth0383a79: renamed from eth0",
		"mmc0: new ultra high speed SDR104 SDXC card at address aaaa",
		// Loop images, optical drives and RAM disks are not the board's storage.
		"blk_update_request: I/O error, dev loop0, sector 0 op 0x0:(READ)",
		"I/O error, dev sr0, sector 0 op 0x0:(READ) flags 0x80700",
		"Buffer I/O error on dev sr0, logical block 0, async page read",
		"I/O error, dev zram0, sector 8 op 0x1:(WRITE)",
		// ext4's periodic restatement of an old error, not a new one.
		"EXT4-fs (mmcblk0p2): error count since last fsck: 3",
		"EXT4-fs (mmcblk0p2): initial error at time 1700000000: ext4_find_entry:1455",
		"EXT4-fs (mmcblk0p2): last error at time 1700000500: ext4_lookup:1820",
		// The kill announcement that precedes the bare line on old kernels.
		"Out of memory: Kill process 812 (java) score 912 or sacrifice child",
	} {
		if ev, ok := ClassifyKernelLine(line); ok {
			t.Errorf("%q classified as %+v", line, ev)
		}
	}
}

// A FIFO stands in for /dev/kmsg: it cannot seek, a read with no writer is
// EOF rather than EAGAIN, and a record can arrive split across writes.
func TestKmsgWatcherOnFIFO(t *testing.T) {
	old := kmsgPath
	defer func() { kmsgPath = old }()
	kmsgPath = filepath.Join(t.TempDir(), "kmsg")
	if err := unix.Mkfifo(kmsgPath, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := OpenKmsg()
	if err != nil {
		t.Fatalf("OpenKmsg on a FIFO: %v", err)
	}
	defer func() { _ = w.Close() }()

	if evs, err := w.Read(); err != nil || len(evs) != 0 {
		t.Fatalf("empty FIFO: %v, %v", evs, err)
	}

	wr, err := os.OpenFile(kmsgPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wr.Close() }()
	write := func(s string) {
		t.Helper()
		if _, err := wr.WriteString(s); err != nil {
			t.Fatal(err)
		}
	}
	usec := func(ago time.Duration) int64 { return int64((monotonicNow() - ago) / time.Microsecond) }
	write(fmt.Sprintf("6,1,%d,-;usb 1-1: new device\n", usec(0)))
	write(fmt.Sprintf("3,2,%d,-;Buffer I/O error on dev mmcblk0p2, logical block 0\n SUBSYSTEM=block\n DEVICE=b179:2\n", usec(0)))
	// User space writing into the log (facility 1: "user") is not the kernel.
	write(fmt.Sprintf("11,3,%d,-;I/O error, dev sdz, sector 1\n", usec(0)))
	write(fmt.Sprintf("2,4,%d,-;Under-volt", usec(time.Minute))) // split mid-record, logged a minute ago
	evs, err := w.Read()
	if err != nil || len(evs) != 1 || evs[0].Device != "mmcblk0" || evs[0].Partition != "mmcblk0p2" {
		t.Fatalf("first read: %+v, %v", evs, err)
	}
	if d := time.Since(evs[0].At); d < 0 || d > 5*time.Second {
		t.Errorf("a fresh record is dated %v ago", d)
	}
	write("age detected! (0x00050005)\n")
	evs, err = w.Read()
	if err != nil || len(evs) != 1 || evs[0].Kind != KernelUnderVoltage {
		t.Fatalf("a record split across writes: %+v, %v", evs, err)
	}
	// Read late (a paused monitor, a backlog), it keeps the time it was logged.
	if d := time.Since(evs[0].At); d < 55*time.Second || d > 70*time.Second {
		t.Errorf("a record logged a minute ago is dated %v ago", d)
	}

	// A writer that never ends its line cannot grow the carry buffer.
	write(strings.Repeat("x", 3*8192))
	if _, err := w.Read(); err != nil || len(w.carry) > len(w.buf) {
		t.Errorf("carry grew to %d bytes (err %v)", len(w.carry), err)
	}
}

// An MMC host's errors count against its card; a host with no block device
// (on a Pi, mmc1 is the SDIO WiFi) is not storage at all.
func TestMMCHostErrorsResolveToTheCard(t *testing.T) {
	oldBase, oldPath := mmcHostBase, kmsgPath
	defer func() { mmcHostBase, kmsgPath = oldBase, oldPath }()
	dir := t.TempDir()
	mmcHostBase = filepath.Join(dir, "mmc_host")
	if err := os.MkdirAll(filepath.Join(mmcHostBase, "mmc0", "mmc0:aaaa", "block", "mmcblk0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mmcHostBase, "mmc1", "mmc1:0001"), 0o755); err != nil {
		t.Fatal(err)
	}
	kmsgPath = filepath.Join(dir, "kmsg")
	if err := os.WriteFile(kmsgPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := OpenKmsg()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	f, err := os.OpenFile(kmsgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("3,1,0,-;mmc0: Timeout waiting for hardware interrupt.\n3,2,0,-;mmc1: Timeout waiting for hardware cmd interrupt.\n")
	_ = f.Close()
	evs, _ := w.Read()
	if len(evs) != 1 || evs[0].Device != "mmcblk0" || evs[0].Partition != "mmc0" {
		t.Fatalf("mmc host errors: %+v", evs)
	}
}

func TestWholeDisk(t *testing.T) {
	for in, want := range map[string]string{
		"mmcblk0p2": "mmcblk0", "mmcblk0": "mmcblk0", "nvme0n1p3": "nvme0n1", "nvme0n1": "nvme0n1",
		"sda1": "sda", "sdab12": "sdab", "sda": "sda", "vdb2": "vdb", "mmcblk0boot0": "mmcblk0boot0",
	} {
		if got := WholeDisk(in); got != want {
			t.Errorf("WholeDisk(%q) = %q, want %q", in, got, want)
		}
	}
}

// What is already in the log when ucmon starts is history: OpenKmsg seeks
// past it, as SEEK_END does on the real /dev/kmsg.
func TestKmsgWatcherSkipsHistory(t *testing.T) {
	old := kmsgPath
	defer func() { kmsgPath = old }()
	kmsgPath = filepath.Join(t.TempDir(), "kmsg")
	if err := os.WriteFile(kmsgPath, []byte("3,1,1,-;I/O error, dev sda, sector 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := OpenKmsg()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()
	if evs, _ := w.Read(); len(evs) != 0 {
		t.Fatalf("an error from before startup was reported: %+v", evs)
	}
	f, err := os.OpenFile(kmsgPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("3,2,2,-;I/O error, dev sda, sector 2\n")
	_ = f.Close()
	if evs, _ := w.Read(); len(evs) != 1 || evs[0].Device != "sda" {
		t.Fatalf("a new error: %+v", evs)
	}
}

func TestCheckKernelLog(t *testing.T) {
	old := kmsgPath
	defer func() { kmsgPath = old }()
	dir := t.TempDir()

	kmsgPath = filepath.Join(dir, "missing")
	if c := CheckKernelLog(); c.Status != CapAbsent {
		t.Errorf("missing log: %+v", c)
	}

	kmsgPath = filepath.Join(dir, "kmsg")
	if err := os.WriteFile(kmsgPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := CheckKernelLog(); c.Status != CapPresent {
		t.Errorf("readable log: %+v", c)
	}

	if os.Geteuid() != 0 {
		if err := os.Chmod(kmsgPath, 0); err != nil {
			t.Fatal(err)
		}
		if c := CheckKernelLog(); c.Status != CapDenied || c.Fix == "" {
			t.Errorf("unreadable log: %+v, want denied with a fix", c)
		}
	}
}

// The real device, where this host allows it: reading must never block, and
// caught-up is EAGAIN, not an error.
func TestKmsgWatcherOnRealLog(t *testing.T) {
	w, err := OpenKmsg()
	if err != nil {
		t.Skipf("/dev/kmsg not readable here: %v", err)
	}
	defer func() { _ = w.Close() }()
	done := make(chan error, 1)
	go func() {
		_, err := w.Read()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Read on /dev/kmsg: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read on /dev/kmsg blocked")
	}
}
