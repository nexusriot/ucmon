package probe

import (
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/shirou/gopsutil/v4/host"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type IfaceInfo struct {
	Name     string
	MTU      int
	Hardware string
	Addrs    []string
	IsUp     bool
	RxBps    float64
	TxBps    float64
	RxTotal  uint64
	TxTotal  uint64
}

type NetSnapshot struct {
	Hostname string
	Uptime   time.Duration
	Ifaces   []IfaceInfo
	TakenAt  time.Time
}

type NetSampler struct {
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

	out := make([]IfaceInfo, 0, len(ifs))
	for _, nif := range ifs {
		ii := IfaceInfo{
			Name:     nif.Name,
			MTU:      nif.MTU,
			Hardware: nif.HardwareAddr.String(),
			IsUp:     (nif.Flags&net.FlagUp != 0),
		}

		addrs, _ := nif.Addrs()
		for _, a := range addrs {
			ii.Addrs = append(ii.Addrs, a.String())
		}

		if c, ok := cur[nif.Name]; ok {
			ii.RxTotal = c.BytesRecv
			ii.TxTotal = c.BytesSent

			if prev, ok2 := s.last[nif.Name]; ok2 {
				ii.RxBps = float64(c.BytesRecv-prev.BytesRecv) / dt
				ii.TxBps = float64(c.BytesSent-prev.BytesSent) / dt
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

// Connection info

type ConnInfo struct {
	Proto      string
	LocalAddr  string
	RemoteAddr string
	Status     string
	PID        int32
	Process    string
}

func ListConnections() ([]ConnInfo, error) {
	conns, err := gnet.Connections("all")
	if err != nil {
		return nil, err
	}

	procNames := map[int32]string{}

	var out []ConnInfo
	for _, c := range conns {
		proto := ""
		switch c.Type {
		case 1:
			proto = "TCP"
		case 2:
			proto = "UDP"
		default:
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
