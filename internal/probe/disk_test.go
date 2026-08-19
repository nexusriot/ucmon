package probe

import (
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
