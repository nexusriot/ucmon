package probe

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// routePath is a var so tests can point it at a fake route table.
var routePath = "/proc/net/route"

// PingResult is one reachability measurement against one target.
type PingResult struct {
	Target string // stable display name ("gateway" or the configured host)
	Addr   string // resolved IPv4 address (may equal Target)
	OK     bool
	RTT    time.Duration
	Method string // "icmp" or "tcp"
}

// LatencySampler measures round-trip time to the default gateway and an
// optional external target. It prefers unprivileged ICMP echo (datagram ICMP
// sockets, allowed by default on Raspberry Pi OS via ping_group_range) and
// falls back to TCP connect timing when ICMP sockets are unavailable.
type LatencySampler struct {
	mu        sync.Mutex
	external  string
	gw        string
	gwChecked time.Time
}

func NewLatencySampler(external string) *LatencySampler {
	return &LatencySampler{external: strings.TrimSpace(external)}
}

// Sample pings each target once, in parallel. The default gateway is
// re-discovered every 30s so network moves are picked up.
func (s *LatencySampler) Sample(timeout time.Duration) []PingResult {
	s.mu.Lock()
	if time.Since(s.gwChecked) > 30*time.Second || s.gwChecked.IsZero() {
		if gw, err := defaultGateway(); err == nil {
			s.gw = gw
		} else {
			s.gw = ""
		}
		s.gwChecked = time.Now()
	}
	gw, ext := s.gw, s.external
	s.mu.Unlock()

	type target struct{ name, addr string }
	var targets []target
	if gw != "" {
		targets = append(targets, target{"gateway", gw})
	}
	if ext != "" {
		targets = append(targets, target{ext, ext})
	}

	results := make([]PingResult, len(targets))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func(i int, t target) {
			defer wg.Done()
			results[i] = pingOnce(t.name, t.addr, timeout)
		}(i, t)
	}
	wg.Wait()
	return results
}

// pingOnce measures one target within timeout. Every step — name resolution
// included — draws from a single deadline, so a wedged resolver costs one
// sample instead of blocking for its whole retry budget (tens of seconds
// against unreachable nameservers) while the next tick queues another.
func pingOnce(name, addr string, timeout time.Duration) PingResult {
	res := PingResult{Target: name, Addr: addr, Method: "icmp"}
	deadline := time.Now().Add(timeout)

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	ip := resolveIPv4(ctx, addr)
	if ip == "" {
		return res // unresolvable now — counts as loss
	}
	res.Addr = ip

	left := time.Until(deadline)
	if left <= 0 {
		return res // resolution consumed the whole budget
	}

	rtt, err := icmpPing(ip, left)
	if err == nil {
		res.OK, res.RTT = true, rtt
		return res
	}
	if !errors.Is(err, errICMPSocket) {
		return res // ICMP worked as a mechanism; the ping itself failed
	}

	// No unprivileged ICMP → time TCP connects. A RST (connection refused)
	// still measures a full round trip, so it counts as success. Each port
	// gets an equal share of whatever time is left.
	res.Method = "tcp"
	ports := []int{80, 443}
	for i, port := range ports {
		left = time.Until(deadline)
		if left <= 0 {
			break
		}
		if rtt, err := tcpPing(ip, port, left/time.Duration(len(ports)-i)); err == nil {
			res.OK, res.RTT = true, rtt
			return res
		}
	}
	return res
}

// resolveIPv4 returns addr if it already is an IPv4 literal, else resolves the
// name under ctx and takes the first IPv4 record.
func resolveIPv4(ctx context.Context, addr string) string {
	if ip := net.ParseIP(addr); ip != nil {
		if ip.To4() != nil {
			return addr
		}
		return "" // IPv6 targets are not supported yet
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", addr)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0].String()
}

// errICMPSocket marks "the ICMP socket could not be created at all" (as
// opposed to a created socket timing out), so callers know to fall back.
var errICMPSocket = errors.New("icmp socket unavailable")

// icmpPing sends one ICMP echo request over an unprivileged datagram ICMP
// socket and waits for the reply. The kernel assigns and matches the echo id
// per socket, so any echo reply read from this socket is ours.
func icmpPing(ip string, timeout time.Duration) (time.Duration, error) {
	addr := net.ParseIP(ip).To4()
	if addr == nil {
		return 0, fmt.Errorf("not an IPv4 address: %s", ip)
	}

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMP)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errICMPSocket, err)
	}
	defer unix.Close(fd)

	tv := unix.NsecToTimeval(timeout.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		return 0, fmt.Errorf("%w: %v", errICMPSocket, err)
	}

	sa := &unix.SockaddrInet4{}
	copy(sa.Addr[:], addr)

	// Echo request: type 8, code 0, checksum, id (kernel-rewritten), seq 1.
	pkt := make([]byte, 16)
	pkt[0] = 8
	binary.BigEndian.PutUint16(pkt[6:8], 1)
	copy(pkt[8:], "ucmonpin")
	binary.BigEndian.PutUint16(pkt[2:4], icmpChecksum(pkt))

	start := time.Now()
	if err := unix.Sendto(fd, pkt, 0, sa); err != nil {
		return 0, err
	}

	buf := make([]byte, 1500)
	deadline := start.Add(timeout)
	for time.Now().Before(deadline) {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return 0, err // EAGAIN on timeout, or a hard error
		}
		if n >= 8 && buf[0] == 0 { // echo reply
			return time.Since(start), nil
		}
		// Some other ICMP type (e.g. destination unreachable): keep
		// reading until the deadline; the recv timeout bounds us.
	}
	return 0, errors.New("timeout")
}

// icmpChecksum is the RFC 1071 internet checksum.
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i:]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

// tcpPing times a TCP connect. A refused connection (RST) is a completed
// round trip and counts as a measurement; only timeouts / route errors fail.
func tcpPing(ip string, port int, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), timeout)
	rtt := time.Since(start)
	if err == nil {
		_ = c.Close()
		return rtt, nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return rtt, nil
	}
	return 0, err
}

// defaultGateway returns the IPv4 default-route gateway from /proc/net/route.
func defaultGateway() (string, error) {
	f, err := os.Open(routePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseDefaultGateway(f)
}

// parseDefaultGateway scans the kernel route table (little-endian hex
// columns) for a default route with the RTF_GATEWAY flag set.
func parseDefaultGateway(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first { // header row
			first = false
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		dest, gw, flags := fields[1], fields[2], fields[3]
		fl, err := strconv.ParseUint(flags, 16, 32)
		if err != nil || dest != "00000000" || fl&0x2 == 0 { // 0x2 = RTF_GATEWAY
			continue
		}
		v, err := strconv.ParseUint(gw, 16, 32)
		if err != nil || v == 0 {
			continue
		}
		return fmt.Sprintf("%d.%d.%d.%d", byte(v), byte(v>>8), byte(v>>16), byte(v>>24)), nil
	}
	return "", errors.New("no default route")
}

// pingGroupRangePath is a var so tests can point it at a fake sysctl file.
var pingGroupRangePath = "/proc/sys/net/ipv4/ping_group_range"

// icmpSocketProbe opens and closes one datagram ICMP socket. It is the only
// honest test of the capability: the sysctl range decides it, but so do
// seccomp filters and container policies that never touch the sysctl.
func icmpSocketProbe() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMP)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

// pingGroupRange parses net.ipv4.ping_group_range ("0 2147483647"), the gid
// range the kernel lets open datagram ICMP sockets.
func pingGroupRange() (lo, hi int64, ok bool) {
	s, ok := sysReadStr(pingGroupRangePath)
	if !ok {
		return 0, 0, false
	}
	fields := strings.Fields(s)
	if len(fields) != 2 {
		return 0, 0, false
	}
	lo, err1 := strconv.ParseInt(fields[0], 10, 64)
	hi, err2 := strconv.ParseInt(fields[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return lo, hi, true
}

// CheckUnprivilegedICMP reports whether the latency sampler can measure a
// real round trip or has to fall back to TCP connect timing — which still
// produces a number, so nothing on screen says the method changed.
func CheckUnprivilegedICMP() Capability {
	lo, hi, rangeOK := pingGroupRange()
	return checkICMP(icmpSocketProbe(), lo, hi, rangeOK, os.Getgid())
}

// checkICMP is the pure half, so the verdict can be tested without the
// kernel's cooperation.
func checkICMP(sockErr error, lo, hi int64, rangeOK bool, gid int) Capability {
	c := Capability{
		Name:    "unprivileged ICMP ping",
		Source:  pingGroupRangePath,
		Affects: "true round-trip times on the Network tab (TCP connect timing is used instead)",
	}

	where := ""
	if rangeOK {
		where = fmt.Sprintf(" (ping_group_range = %d %d, gid %d)", lo, hi, gid)
	}

	if sockErr == nil {
		c.Status = CapPresent
		c.Detail = "datagram ICMP sockets allowed" + where
		return c
	}

	c.Status = CapDenied
	if !errors.Is(sockErr, unix.EACCES) && !errors.Is(sockErr, unix.EPERM) {
		c.Status = CapAbsent
	}
	c.Detail = "cannot open a datagram ICMP socket: " + sockErr.Error() + where
	c.Fix = "sudo sysctl -w net.ipv4.ping_group_range=\"0 2147483647\"\n" +
		"echo 'net.ipv4.ping_group_range = 0 2147483647' | sudo tee /etc/sysctl.d/90-ucmon-ping.conf"
	return c
}
