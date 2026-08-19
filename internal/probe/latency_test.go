package probe

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const routeSample = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
wlan0	00000000	0101A8C0	0003	0	0	600	00000000	0	0	0
wlan0	0001A8C0	00000000	0001	0	0	600	00FFFFFF	0	0	0
`

func TestParseDefaultGateway(t *testing.T) {
	gw, err := parseDefaultGateway(strings.NewReader(routeSample))
	if err != nil {
		t.Fatalf("parseDefaultGateway: %v", err)
	}
	if gw != "192.168.1.1" { // 0101A8C0 little-endian
		t.Errorf("gateway = %q, want 192.168.1.1", gw)
	}
}

func TestParseDefaultGatewayNoDefault(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	if _, err := parseDefaultGateway(strings.NewReader(content)); err == nil {
		t.Error("expected an error without a default route")
	}
}

// The internet checksum of a packet that already carries its checksum is 0.
func TestICMPChecksumProperty(t *testing.T) {
	pkt := make([]byte, 16)
	pkt[0] = 8
	pkt[7] = 1
	copy(pkt[8:], "ucmonpin")
	cs := icmpChecksum(pkt)
	pkt[2] = byte(cs >> 8)
	pkt[3] = byte(cs)
	if got := icmpChecksum(pkt); got != 0 {
		t.Errorf("checksum over checksummed packet = %#x, want 0", got)
	}
}

func TestTCPPingSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	port := ln.Addr().(*net.TCPAddr).Port
	rtt, err := tcpPing("127.0.0.1", port, time.Second)
	if err != nil {
		t.Fatalf("tcpPing open port: %v", err)
	}
	if rtt <= 0 {
		t.Errorf("rtt = %v", rtt)
	}
}

// A refused connection is a completed round trip — it must count as a
// measurement, not a loss.
func TestTCPPingRefusedCountsAsMeasurement(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // now nothing listens on that port

	rtt, err := tcpPing("127.0.0.1", port, time.Second)
	if err != nil {
		t.Fatalf("refused should not be an error, got %v", err)
	}
	if rtt <= 0 {
		t.Errorf("rtt = %v", rtt)
	}
}

func TestICMPPingLoopback(t *testing.T) {
	rtt, err := icmpPing("127.0.0.1", time.Second)
	if err != nil {
		if errors.Is(err, errICMPSocket) {
			t.Skip("unprivileged ICMP sockets unavailable in this environment")
		}
		t.Skipf("loopback ping failed (restricted environment): %v", err)
	}
	if rtt <= 0 {
		t.Errorf("rtt = %v", rtt)
	}
}

func TestResolveIPv4(t *testing.T) {
	ctx := context.Background()
	if got := resolveIPv4(ctx, "192.168.1.1"); got != "192.168.1.1" {
		t.Errorf("v4 literal: %q", got)
	}
	if got := resolveIPv4(ctx, "::1"); got != "" {
		t.Errorf("v6 literal should be rejected, got %q", got)
	}
}

// An IP literal needs no resolver, so an already-cancelled context must not
// stop it — only a name lookup is bound by the deadline.
func TestResolveIPv4LiteralIgnoresContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := resolveIPv4(ctx, "10.0.0.1"); got != "10.0.0.1" {
		t.Errorf("literal with a cancelled context: %q", got)
	}
}

// A name lookup must give up with the deadline instead of running for the
// resolver's full retry budget.
func TestResolveIPv4RespectsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := resolveIPv4(ctx, "ucmon-does-not-exist.invalid")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("resolution took %v, deadline was 20ms", elapsed)
	}
	if got != "" {
		t.Errorf("unresolvable name returned %q", got)
	}
}

// pingOnce must honour its timeout even when the name never resolves.
func TestPingOnceBoundedByTimeout(t *testing.T) {
	start := time.Now()
	res := pingOnce("target", "ucmon-does-not-exist.invalid", 100*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > 3*time.Second {
		t.Errorf("pingOnce took %v for a 100ms budget", elapsed)
	}
	if res.OK {
		t.Error("unresolvable target reported OK")
	}
}

func TestSamplerSkipsExternalWhenUnset(t *testing.T) {
	s := NewLatencySampler("   ")
	if s.external != "" {
		t.Errorf("external = %q, want trimmed empty", s.external)
	}
}

// With no default route and no external target there is nothing to ping.
func TestSampleNoTargets(t *testing.T) {
	path := t.TempDir() + "/route"
	if err := writeFile(path, "Iface\tDestination\tGateway\tFlags\n"); err != nil {
		t.Fatal(err)
	}
	old := routePath
	routePath = path
	defer func() { routePath = old }()

	s := NewLatencySampler("")
	if results := s.Sample(50 * time.Millisecond); len(results) != 0 {
		t.Errorf("expected no results, got %+v", results)
	}
}

// The gateway target is discovered from the route table; the ping itself may
// fail here (fake gateway), but the target identity must be right.
func TestSampleDiscoversGateway(t *testing.T) {
	path := t.TempDir() + "/route"
	if err := writeFile(path, routeSample); err != nil {
		t.Fatal(err)
	}
	old := routePath
	routePath = path
	defer func() { routePath = old }()

	s := NewLatencySampler("")
	results := s.Sample(50 * time.Millisecond)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Target != "gateway" || results[0].Addr != "192.168.1.1" {
		t.Errorf("result = %+v", results[0])
	}
}

// An unresolvable target counts as loss, quickly and without network access.
func TestPingOnceUnresolvable(t *testing.T) {
	res := pingOnce("v6", "::1", 50*time.Millisecond)
	if res.OK {
		t.Errorf("IPv6 target should be a miss (unsupported), got %+v", res)
	}
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}
