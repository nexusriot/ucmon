package probe

import (
	"syscall"
	"testing"

	gnet "github.com/shirou/gopsutil/v4/net"
)

func TestProtoLabel(t *testing.T) {
	cases := []struct {
		typ, family uint32
		want        string
	}{
		{syscall.SOCK_STREAM, syscall.AF_INET, "TCP"},
		{syscall.SOCK_DGRAM, syscall.AF_INET, "UDP"},
		{syscall.SOCK_STREAM, syscall.AF_INET6, "TCP6"},
		{syscall.SOCK_DGRAM, syscall.AF_INET6, "UDP6"},
		{syscall.SOCK_RAW, syscall.AF_INET, ""}, // skipped
	}
	for _, c := range cases {
		if got := protoLabel(c.typ, c.family); got != c.want {
			t.Errorf("protoLabel(%d,%d) = %q, want %q", c.typ, c.family, got, c.want)
		}
	}
}

func TestConvertConns(t *testing.T) {
	stats := []gnet.ConnectionStat{
		{
			Type:   syscall.SOCK_STREAM,
			Family: syscall.AF_INET,
			Laddr:  gnet.Addr{IP: "0.0.0.0", Port: 22},
			Status: "LISTEN",
		},
		{
			Type:   syscall.SOCK_DGRAM,
			Family: syscall.AF_INET6,
			Laddr:  gnet.Addr{IP: "::", Port: 5353},
			Raddr:  gnet.Addr{IP: "ff02::fb", Port: 5353},
			Status: "NONE",
		},
		{
			Type:   syscall.SOCK_RAW, // unsupported type — skipped
			Family: syscall.AF_INET,
			Laddr:  gnet.Addr{IP: "0.0.0.0", Port: 0},
		},
	}

	out := convertConns(stats)
	if len(out) != 2 {
		t.Fatalf("got %d conns, want 2 (raw skipped)", len(out))
	}
	if out[0].Proto != "TCP" || out[0].LocalAddr != "0.0.0.0:22" {
		t.Errorf("conn[0] = %+v", out[0])
	}
	if out[0].RemoteAddr != "-" {
		t.Errorf("empty remote should render '-', got %q", out[0].RemoteAddr)
	}
	if out[1].Proto != "UDP6" || out[1].RemoteAddr != "ff02::fb:5353" {
		t.Errorf("conn[1] = %+v", out[1])
	}
}

// Smoke tests against the real system: the samplers must return sane data on
// any Linux host.
func TestSystemProbesSmoke(t *testing.T) {
	if snap, err := SampleMem(); err != nil || snap.Total == 0 {
		t.Errorf("SampleMem: %+v err=%v", snap, err)
	}
	if snap, err := SamplePower(); err != nil || snap.Load.CPUCount == 0 {
		t.Errorf("SamplePower: %+v err=%v", snap, err)
	}

	ns := NewNetSampler()
	if _, err := ns.Sample(); err != nil {
		t.Errorf("NetSampler.Sample: %v", err)
	}
	snap, err := ns.Sample()
	if err != nil || len(snap.Ifaces) == 0 {
		t.Errorf("second net sample: %d ifaces, err=%v", len(snap.Ifaces), err)
	}

	ds := NewDiskSampler()
	if disks, _, err := ds.Sample(); err != nil || len(disks) == 0 {
		t.Errorf("DiskSampler.Sample: %d disks, err=%v", len(disks), err)
	}

	_ = ReadPSI() // must not panic whether or not the kernel has PSI
}

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{1536 * 1024, "1.5 MiB"},
	}
	for _, c := range cases {
		if got := HumanBytes(c.in); got != c.want {
			t.Errorf("HumanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
