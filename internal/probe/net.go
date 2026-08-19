package probe

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type IfaceInfo struct {
	Name  string
	Addrs []string
	IsUp  bool
	RxBps float64
	TxBps float64
	WiFi  WiFiInfo // populated for wireless interfaces (WiFi.Present)
}

type NetSnapshot struct {
	Hostname string
	Uptime   time.Duration
	Ifaces   []IfaceInfo
	TakenAt  time.Time
}

type NetSampler struct {
	mu     sync.Mutex
	last   map[string]gnet.IOCountersStat
	lastAt time.Time
}

func NewNetSampler() *NetSampler {
	return &NetSampler{
		last:   map[string]gnet.IOCountersStat{},
		lastAt: time.Time{},
	}
}

func (s *NetSampler) Sample() (NetSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	hi, _ := host.Info()
	hostName := ""
	uptime := time.Duration(0)
	if hi != nil {
		hostName = hi.Hostname
		uptime = time.Duration(hi.Uptime) * time.Second
	}

	ifs, err := net.Interfaces()
	if err != nil {
		return NetSnapshot{}, err
	}

	counters, err := gnet.IOCounters(true)
	if err != nil {
		return NetSnapshot{}, err
	}
	cur := map[string]gnet.IOCountersStat{}
	for _, c := range counters {
		cur[c.Name] = c
	}

	dt := now.Sub(s.lastAt).Seconds()
	if dt <= 0 {
		dt = 1
	}

	wifi := readWirelessStats()

	out := make([]IfaceInfo, 0, len(ifs))
	for _, nif := range ifs {
		ii := IfaceInfo{
			Name: nif.Name,
			IsUp: (nif.Flags&net.FlagUp != 0),
		}
		if w, ok := wifi[nif.Name]; ok {
			ii.WiFi = w
		}

		addrs, _ := nif.Addrs()
		for _, a := range addrs {
			ii.Addrs = append(ii.Addrs, a.String())
		}

		if c, ok := cur[nif.Name]; ok {
			if prev, ok2 := s.last[nif.Name]; ok2 {
				ii.RxBps = CounterRate(c.BytesRecv, prev.BytesRecv, dt)
				ii.TxBps = CounterRate(c.BytesSent, prev.BytesSent, dt)
			}
		}

		out = append(out, ii)
	}

	s.last = cur
	s.lastAt = now

	return NetSnapshot{
		Hostname: hostName,
		Uptime:   uptime,
		Ifaces:   out,
		TakenAt:  now,
	}, nil
}

type ConnInfo struct {
	Proto      string
	LocalAddr  string
	RemoteAddr string
	Status     string
	PID        int32
	Process    string
}

// protoLabel maps a socket type + address family to a display label.
// Unknown types yield "" (the connection is skipped).
func protoLabel(typ, family uint32) string {
	proto := ""
	switch typ {
	case syscall.SOCK_STREAM:
		proto = "TCP"
	case syscall.SOCK_DGRAM:
		proto = "UDP"
	default:
		return ""
	}
	if family == syscall.AF_INET6 {
		proto += "6"
	}
	return proto
}

// convertConns maps gopsutil connection stats to ConnInfo rows, resolving
// each PID's process name once.
func convertConns(conns []gnet.ConnectionStat) []ConnInfo {
	procNames := map[int32]string{}

	var out []ConnInfo
	for _, c := range conns {
		proto := protoLabel(c.Type, c.Family)
		if proto == "" {
			continue
		}

		local := fmt.Sprintf("%s:%d", c.Laddr.IP, c.Laddr.Port)
		remote := fmt.Sprintf("%s:%d", c.Raddr.IP, c.Raddr.Port)
		if c.Raddr.IP == "" {
			remote = "-"
		}

		name := ""
		if c.Pid > 0 {
			if cached, ok := procNames[c.Pid]; ok {
				name = cached
			} else {
				if p, err := process.NewProcess(c.Pid); err == nil {
					name, _ = p.Name()
				}
				procNames[c.Pid] = name
			}
		}

		out = append(out, ConnInfo{
			Proto:      proto,
			LocalAddr:  local,
			RemoteAddr: remote,
			Status:     c.Status,
			PID:        c.Pid,
			Process:    name,
		})
	}
	return out
}

func ListConnections() ([]ConnInfo, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return nil, err
	}

	out := convertConns(conns)

	sort.Slice(out, func(i, j int) bool {
		if out[i].Status != out[j].Status {
			if out[i].Status == "LISTEN" {
				return true
			}
			if out[j].Status == "LISTEN" {
				return false
			}
		}
		return out[i].LocalAddr < out[j].LocalAddr
	})

	return out, nil
}

// ConnectionsForPID returns the sockets owned by one process (best effort;
// nil when unreadable).
func ConnectionsForPID(pid int32) []ConnInfo {
	conns, err := gnet.ConnectionsPid("all", pid)
	if err != nil {
		return nil
	}
	return convertConns(conns)
}
