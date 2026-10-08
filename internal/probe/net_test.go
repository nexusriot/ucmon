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

// A Unix socket carries the same SOCK_STREAM type as a TCP one; only the
// address family tells them apart. Keying on the type alone filed
// /run/dbus/system_bus_socket in the connection table as "TCP".
func TestProtoLabelUnixSockets(t *testing.T) {
	cases := []struct {
		typ, family uint32
		want        string
	}{
		{syscall.SOCK_STREAM, syscall.AF_UNIX, "UNIX"},
		{syscall.SOCK_DGRAM, syscall.AF_UNIX, "UNIX"},
		{syscall.SOCK_SEQPACKET, syscall.AF_UNIX, "UNIX"},
		{syscall.SOCK_RAW, syscall.AF_UNIX, ""},
	}
	for _, c := range cases {
		if got := protoLabel(c.typ, c.family); got != c.want {
			t.Errorf("protoLabel(%d, AF_UNIX) = %q, want %q", c.typ, got, c.want)
		}
	}
}

// A Unix socket has a path, not an address and port, so the "ip:port" format
// would render it as "/run/dbus/system_bus_socket:0".
func TestConvertConnsFormatsUnixPaths(t *testing.T) {
	stats := []gnet.ConnectionStat{
		{Family: syscall.AF_UNIX, Type: syscall.SOCK_STREAM, Status: "NONE",
			Laddr: gnet.Addr{IP: "/run/dbus/system_bus_socket"}},
		{Family: syscall.AF_UNIX, Type: syscall.SOCK_STREAM, Status: "NONE"},
	}
	out := convertConns(stats)
	if len(out) != 2 {
		t.Fatalf("got %d rows, want 2", len(out))
	}
	if out[0].Proto != "UNIX" || out[0].LocalAddr != "/run/dbus/system_bus_socket" {
		t.Errorf("named socket = %+v", out[0])
	}
	if out[1].LocalAddr != "(unnamed)" {
		t.Errorf("unnamed socket LocalAddr = %q, want (unnamed)", out[1].LocalAddr)
	}
}

// The host connection table is inet-only: Unix sockets are a third of the
// rows on a desktop and carry nothing the table's columns can show.
func TestListConnectionsHasNoUnixRows(t *testing.T) {
	conns, err := ListConnections()
	if err != nil {
		t.Skipf("ListConnections unavailable here: %v", err)
	}
	for _, c := range conns {
		if c.Proto == "UNIX" {
			t.Fatalf("Unix socket leaked into the host connection table: %+v", c)
		}
	}
}

func TestIfaceBadFrames(t *testing.T) {
	ii := IfaceInfo{
		ErrIn: 1, ErrOut: 2, DropIn: 3, DropOut: 4,
		ErrInPs: 0.5, ErrOutPs: 1.5, DropInPs: 2, DropOutPs: 0,
	}
	if got := ii.BadFramesTotal(); got != 10 {
		t.Errorf("BadFramesTotal() = %d, want 10", got)
	}
	if got := ii.BadFrames(); got != 4 {
		t.Errorf("BadFrames() = %v, want 4", got)
	}
	if (IfaceInfo{}).BadFrames() != 0 {
		t.Error("a clean interface reported bad frames")
	}
}

// The sampler must carry the error and drop counters it already receives:
// a link passing traffic while dropping frames is invisible in throughput.
func TestNetSamplerCarriesErrorCounters(t *testing.T) {
	s := NewNetSampler()
	snap, err := s.Sample()
	if err != nil {
		t.Skipf("net sampling unavailable here: %v", err)
	}
	for _, ii := range snap.Ifaces {
		if ii.HasCounter {
			return // at least one interface reported counters; the wiring works
		}
	}
	t.Error("no interface carried counters; Errin/Dropin are being discarded")
}
