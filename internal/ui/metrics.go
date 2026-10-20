package ui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nexusriot/ucmon/internal/probe"
)

// SnapshotJSON is the machine-readable form of a ucmon snapshot: the same
// readings the text report carries, in a shape something else can consume.
// The text report is for a person; parsing it in a script means re-deriving
// its layout every time the layout changes.
type SnapshotJSON struct {
	Version   string             `json:"version"`
	Timestamp time.Time          `json:"timestamp"`
	Host      string             `json:"host,omitempty"`
	UptimeSec float64            `json:"uptime_sec,omitempty"`
	CPU       cpuJSON            `json:"cpu"`
	Memory    memJSON            `json:"memory"`
	Load      loadJSON           `json:"load"`
	Pressure  map[string]psiJSON `json:"pressure,omitempty"`
	Battery   *batteryJSON       `json:"battery,omitempty"`
	Throttle  *throttleJSON      `json:"throttle,omitempty"`
	Disks     []diskJSON         `json:"disks,omitempty"`
	Storage   []storageJSON      `json:"storage,omitempty"`
	Network   []ifaceJSON        `json:"network,omitempty"`
	Latency   []latencyJSON      `json:"latency,omitempty"`
	Modem     *modemJSON         `json:"modem,omitempty"`
	GPU       *gpuJSON           `json:"gpu,omitempty"`
	TopProcs  []procJSON         `json:"top_processes,omitempty"`
	Peaks     peaksJSON          `json:"session_peaks"`
	Alerts    []alertJSON        `json:"active_alerts"`
}

type cpuJSON struct {
	TotalPct   float64            `json:"total_pct"`
	PerCore    []float64          `json:"per_core_pct,omitempty"`
	Breakdown  map[string]float64 `json:"breakdown_pct,omitempty"`
	Busiest    string             `json:"busiest,omitempty"`
	Temps      map[string]float64 `json:"temps_c,omitempty"`
	Governors  map[string]string  `json:"governors,omitempty"`
	ClocksMHz  map[string]float64 `json:"clocks_mhz,omitempty"`
	ThreadsRun int                `json:"-"`
}

type memJSON struct {
	TotalB     uint64  `json:"total_bytes"`
	UsedB      uint64  `json:"used_bytes"`
	AvailableB uint64  `json:"available_bytes"`
	UsedPct    float64 `json:"used_pct"`
	SwapTotalB uint64  `json:"swap_total_bytes"`
	SwapUsedB  uint64  `json:"swap_used_bytes"`
	SwapPct    float64 `json:"swap_used_pct"`
}

type loadJSON struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	CPUs   int     `json:"cpus"`
}

type psiJSON struct {
	SomeAvg10 float64 `json:"some_avg10"`
	SomeAvg60 float64 `json:"some_avg60"`
	FullAvg10 float64 `json:"full_avg10,omitempty"`
}

type batteryJSON struct {
	Name       string  `json:"name,omitempty"`
	Pct        int     `json:"percent"`
	Status     string  `json:"status,omitempty"`
	VoltageV   float64 `json:"voltage_v,omitempty"`
	PowerW     float64 `json:"power_w,omitempty"`
	HealthPct  float64 `json:"health_pct,omitempty"`
	SecondsLef float64 `json:"seconds_left,omitempty"`
	ACOnline   *bool   `json:"ac_online,omitempty"`
}

type throttleJSON struct {
	Raw              uint64 `json:"raw"`
	UnderVoltageNow  bool   `json:"under_voltage_now"`
	ThrottledNow     bool   `json:"throttled_now"`
	FreqCappedNow    bool   `json:"freq_capped_now"`
	SoftTempLimitNow bool   `json:"soft_temp_limit_now"`
}

type diskJSON struct {
	Mount    string  `json:"mount"`
	Device   string  `json:"device,omitempty"`
	FSType   string  `json:"fstype,omitempty"`
	TotalB   uint64  `json:"total_bytes"`
	UsedB    uint64  `json:"used_bytes"`
	UsedPct  float64 `json:"used_pct"`
	ReadBps  float64 `json:"read_bps,omitempty"`
	WriteBps float64 `json:"write_bps,omitempty"`
}

type storageJSON struct {
	Device   string  `json:"device"`
	Kind     string  `json:"kind,omitempty"`
	Name     string  `json:"name,omitempty"`
	SizeB    uint64  `json:"size_bytes,omitempty"`
	WearPct  float64 `json:"wear_pct,omitempty"`
	PreEOL   string  `json:"pre_eol,omitempty"`
	ReadOnly bool    `json:"read_only,omitempty"`
	Severity int     `json:"severity"`
}

type ifaceJSON struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Addrs     []string `json:"addrs,omitempty"`
	RxBps     float64  `json:"rx_bps"`
	TxBps     float64  `json:"tx_bps"`
	ErrIn     uint64   `json:"err_in,omitempty"`
	ErrOut    uint64   `json:"err_out,omitempty"`
	DropIn    uint64   `json:"drop_in,omitempty"`
	DropOut   uint64   `json:"drop_out,omitempty"`
	BadPs     float64  `json:"bad_frames_per_sec,omitempty"`
	WiFiPct   float64  `json:"wifi_quality_pct,omitempty"`
	WiFiDBm   float64  `json:"wifi_signal_dbm,omitempty"`
	SSID      string   `json:"ssid,omitempty"`
	ChannelNo int      `json:"channel,omitempty"`
}

type latencyJSON struct {
	Target  string  `json:"target"`
	Addr    string  `json:"addr,omitempty"`
	OK      bool    `json:"ok"`
	RTTms   float64 `json:"rtt_ms,omitempty"`
	LossPct float64 `json:"loss_pct"`
	Method  string  `json:"method,omitempty"`
}

type modemJSON struct {
	Model      string  `json:"model,omitempty"`
	State      string  `json:"state,omitempty"`
	Registered bool    `json:"registered"`
	Operator   string  `json:"operator,omitempty"`
	AccessTech string  `json:"access_tech,omitempty"`
	SignalPct  int     `json:"signal_pct,omitempty"`
	RSSIdBm    float64 `json:"rssi_dbm,omitempty"`
}

type gpuJSON struct {
	CoreTempC float64            `json:"core_temp_c,omitempty"`
	CoreVolts float64            `json:"core_volts,omitempty"`
	MemARMMB  int                `json:"mem_arm_mb,omitempty"`
	MemGPUMB  int                `json:"mem_gpu_mb,omitempty"`
	Clocks    map[string]float64 `json:"clocks_mhz,omitempty"`
}

type procJSON struct {
	PID        int32   `json:"pid"`
	Name       string  `json:"name"`
	User       string  `json:"user,omitempty"`
	CPUPct     float64 `json:"cpu_pct"`
	MemPct     float64 `json:"mem_pct"`
	RSSB       uint64  `json:"rss_bytes"`
	IOReadBps  float64 `json:"io_read_bps,omitempty"`
	IOWriteBps float64 `json:"io_write_bps,omitempty"`
}

type peaksJSON struct {
	CPUPct    float64 `json:"cpu_pct"`
	TempC     float64 `json:"temp_c,omitempty"`
	TempLabel string  `json:"temp_label,omitempty"`
	Load1     float64 `json:"load1"`
	MemPct    float64 `json:"mem_pct"`
	BattMin   int     `json:"battery_min_pct,omitempty"`
}

type alertJSON struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// snapshotJSON builds the machine-readable snapshot from the current model.
func (m Model) snapshotJSON(now time.Time) SnapshotJSON {
	s := SnapshotJSON{
		Version:   version,
		Timestamp: now,
		Host:      m.netSnap.Hostname,
		UptimeSec: m.netSnap.Uptime.Seconds(),
		CPU: cpuJSON{
			TotalPct: m.cpuSnap.TotalPercent,
			PerCore:  m.cpuSnap.PerCorePercent,
		},
		Memory: memJSON{
			TotalB:     m.memSnap.Total,
			UsedB:      m.memSnap.Used,
			AvailableB: m.memSnap.Available,
			UsedPct:    m.memSnap.UsedPct,
			SwapTotalB: m.memSnap.SwapTotal,
			SwapUsedB:  m.memSnap.SwapUsed,
			SwapPct:    m.memSnap.SwapUsedPct,
		},
		Load: loadJSON{
			Load1: m.powerSnap.Load.Load1, Load5: m.powerSnap.Load.Load5,
			Load15: m.powerSnap.Load.Load15, CPUs: m.powerSnap.Load.CPUCount,
		},
		Peaks: peaksJSON{
			CPUPct: m.peaks.cpuPct, TempC: m.peaks.tempC, TempLabel: m.peaks.tempLabel,
			Load1: m.peaks.load1, MemPct: m.peaks.memPct, BattMin: m.peaks.battMin,
		},
		Alerts: []alertJSON{},
	}

	if b := m.cpuSnap.Breakdown; b.Valid {
		s.CPU.Breakdown = map[string]float64{
			"user": b.User, "system": b.System, "nice": b.Nice,
			"iowait": b.Iowait, "irq": b.IRQ, "steal": b.Steal, "idle": b.Idle,
		}
		s.CPU.Busiest = b.Busiest
	}
	if len(m.cpuSnap.Temperatures) > 0 {
		s.CPU.Temps = map[string]float64{}
		for _, t := range m.cpuSnap.Temperatures {
			s.CPU.Temps[t.Label] = t.Temp
		}
	}
	if len(m.cpuSnap.Freqs) > 0 {
		s.CPU.Governors = map[string]string{}
		s.CPU.ClocksMHz = map[string]float64{}
		for _, f := range m.cpuSnap.Freqs {
			s.CPU.Governors[f.Policy] = f.Governor
			s.CPU.ClocksMHz[f.Policy] = f.CurMHz
		}
	}

	if m.psi.Present {
		s.Pressure = map[string]psiJSON{}
		for name, r := range map[string]probe.PSIRes{
			"cpu": m.psi.CPU, "memory": m.psi.Mem, "io": m.psi.IO,
		} {
			if r.HasSome {
				s.Pressure[name] = psiJSON{r.SomeAvg10, r.SomeAvg60, r.FullAvg10}
			}
		}
	}

	if b := m.powerSnap.Battery; b.Present {
		bj := &batteryJSON{
			Name: b.Name, Pct: b.Capacity, Status: b.Status,
			VoltageV: b.VoltageV, PowerW: b.PowerW, HealthPct: b.HealthPct,
			SecondsLef: b.TimeLeft.Seconds(),
		}
		if m.powerSnap.ACKnown {
			online := m.powerSnap.ACOnline
			bj.ACOnline = &online
		}
		s.Battery = bj
	}
	if t := m.powerSnap.Throttle; t.Available {
		s.Throttle = &throttleJSON{t.Raw, t.UnderVoltageNow, t.ThrottledNow,
			t.FreqCappedNow, t.SoftTempLimitNow}
	}

	ioByDev := map[string]probe.DiskIOInfo{}
	for _, io := range m.diskIOs {
		ioByDev[io.Device] = io
	}
	for _, d := range m.disks {
		dj := diskJSON{Mount: d.MountPoint, Device: d.Device, FSType: d.FSType,
			TotalB: d.Total, UsedB: d.Used, UsedPct: d.UsedPct}
		if io, ok := diskIOFor(ioByDev, d.Device); ok {
			dj.ReadBps, dj.WriteBps = io.ReadBps, io.WriteBps
		}
		s.Disks = append(s.Disks, dj)
	}

	for _, h := range m.storage {
		sj := storageJSON{Device: h.Device, Kind: h.Kind, Name: h.Name,
			SizeB: h.SizeB, PreEOL: h.EOLLabel(), ReadOnly: h.ReadOnly,
			Severity: h.Severity()}
		if pct, ok := h.WearPct(); ok {
			sj.WearPct = pct
		}
		s.Storage = append(s.Storage, sj)
	}

	for _, ii := range m.netSnap.Ifaces {
		if !ii.IsUp {
			continue
		}
		ij := ifaceJSON{Name: ii.Name, Up: ii.IsUp, Addrs: ii.Addrs,
			RxBps: ii.RxBps, TxBps: ii.TxBps,
			ErrIn: ii.ErrIn, ErrOut: ii.ErrOut, DropIn: ii.DropIn, DropOut: ii.DropOut,
			BadPs: ii.BadFrames()}
		if ii.WiFi.Present {
			ij.WiFiPct, ij.WiFiDBm = ii.WiFi.QualityPct, ii.WiFi.SignalDBm
		}
		if link, ok := m.iwLinks[ii.Name]; ok {
			ij.SSID, ij.ChannelNo = link.SSID, link.Channel
		}
		s.Network = append(s.Network, ij)
	}

	for _, r := range m.pings {
		lj := latencyJSON{Target: r.Target, Addr: r.Addr, OK: r.OK, Method: r.Method}
		if r.OK {
			lj.RTTms = float64(r.RTT.Microseconds()) / 1000
		}
		if win := m.lossWin[r.Target]; len(win) > 0 {
			lj.LossPct = lossPct(win)
		}
		s.Latency = append(s.Latency, lj)
	}

	if m.modem.Present {
		s.Modem = &modemJSON{Model: m.modem.Model, State: m.modem.State,
			Registered: m.modem.Registered(), Operator: m.modem.Operator,
			AccessTech: m.modem.AccessTech, SignalPct: m.modem.SignalPct,
			RSSIdBm: m.modem.RSSIdBm}
	}

	if m.gpuSnap.Available {
		gj := &gpuJSON{CoreTempC: m.gpuSnap.CoreTempC, CoreVolts: m.gpuSnap.CoreVolts,
			MemARMMB: m.gpuSnap.MemARMMB, MemGPUMB: m.gpuSnap.MemGPUMB}
		if len(m.gpuSnap.Clocks) > 0 {
			gj.Clocks = map[string]float64{}
			for _, c := range m.gpuSnap.Clocks {
				gj.Clocks[c.Name] = c.MHz
			}
		}
		s.GPU = gj
	}

	for _, p := range m.topProcs(10) {
		s.TopProcs = append(s.TopProcs, procJSON{PID: p.PID, Name: p.Name, User: p.User,
			CPUPct: p.CPUPct, MemPct: float64(p.MemPct), RSSB: p.MemRSS,
			IOReadBps: p.IOReadBps, IOWriteBps: p.IOWriteBps})
	}

	for _, a := range m.alerts.activeList() {
		s.Alerts = append(s.Alerts, alertJSON{Severity: a.sev.label(), Message: a.msg})
	}

	return s
}

// buildJSONReport renders the snapshot as indented JSON with a trailing
// newline, so it behaves on a terminal and in a pipe alike.
func (m Model) buildJSONReport(now time.Time) (string, error) {
	b, err := json.MarshalIndent(m.snapshotJSON(now), "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}

// buildPrometheus renders the snapshot in the Prometheus text exposition
// format. Everything it needs is already collected for the report; emitting
// it costs nothing and turns a one-off glance into something graphable.
func (m Model) buildPrometheus() string {
	var b strings.Builder
	w := func(name, help, typ string, samples [][2]string) {
		if len(samples) == 0 {
			return
		}
		fmt.Fprintf(&b, "# HELP ucmon_%s %s\n# TYPE ucmon_%s %s\n", name, help, name, typ)
		for _, s := range samples {
			fmt.Fprintf(&b, "ucmon_%s%s %s\n", name, s[0], s[1])
		}
	}
	num := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

	w("cpu_usage_percent", "Total CPU utilisation.", "gauge",
		[][2]string{{"", num(m.cpuSnap.TotalPercent)}})

	if bd := m.cpuSnap.Breakdown; bd.Valid {
		var ss [][2]string
		for _, c := range []struct {
			mode string
			v    float64
		}{
			{"user", bd.User}, {"system", bd.System}, {"nice", bd.Nice},
			{"iowait", bd.Iowait}, {"irq", bd.IRQ}, {"steal", bd.Steal}, {"idle", bd.Idle},
		} {
			ss = append(ss, [2]string{fmt.Sprintf("{mode=%q}", c.mode), num(c.v)})
		}
		w("cpu_mode_percent", "CPU time by mode over the last interval.", "gauge", ss)
	}

	var temps [][2]string
	for _, t := range m.cpuSnap.Temperatures {
		temps = append(temps, [2]string{fmt.Sprintf("{sensor=%q}", t.Label), num(t.Temp)})
	}
	sort.Slice(temps, func(i, j int) bool { return temps[i][0] < temps[j][0] })
	w("temperature_celsius", "Temperature sensor readings.", "gauge", temps)

	w("memory_used_percent", "RAM in use.", "gauge",
		[][2]string{{"", num(m.memSnap.UsedPct)}})
	w("memory_used_bytes", "RAM in use.", "gauge",
		[][2]string{{"", num(float64(m.memSnap.Used))}})
	w("memory_total_bytes", "Installed RAM.", "gauge",
		[][2]string{{"", num(float64(m.memSnap.Total))}})
	if m.memSnap.SwapTotal > 0 {
		w("swap_used_percent", "Swap in use.", "gauge",
			[][2]string{{"", num(m.memSnap.SwapUsedPct)}})
	}

	w("load1", "1-minute load average.", "gauge",
		[][2]string{{"", num(m.powerSnap.Load.Load1)}})

	var disks, diskBytes [][2]string
	for _, d := range m.disks {
		lbl := fmt.Sprintf("{mount=%q,fstype=%q}", d.MountPoint, d.FSType)
		disks = append(disks, [2]string{lbl, num(d.UsedPct)})
		// d.Free, not Total-Used: the two differ by the root-reserved blocks,
		// and this has to agree with the Disk tab and the JSON snapshot.
		diskBytes = append(diskBytes, [2]string{lbl, num(float64(d.Free))})
	}
	w("filesystem_used_percent", "Filesystem utilisation.", "gauge", disks)
	w("filesystem_free_bytes", "Filesystem free space.", "gauge", diskBytes)

	var rx, tx, bad [][2]string
	for _, ii := range m.netSnap.Ifaces {
		if !ii.IsUp {
			continue
		}
		lbl := fmt.Sprintf("{device=%q}", ii.Name)
		rx = append(rx, [2]string{lbl, num(ii.RxBps)})
		tx = append(tx, [2]string{lbl, num(ii.TxBps)})
		bad = append(bad, [2]string{lbl, num(float64(ii.BadFramesTotal()))})
	}
	w("network_receive_bytes_per_second", "Interface receive rate.", "gauge", rx)
	w("network_transmit_bytes_per_second", "Interface transmit rate.", "gauge", tx)
	w("network_bad_frames_total", "Interface errors plus drops since boot.", "counter", bad)

	var wifi [][2]string
	for _, ii := range m.netSnap.Ifaces {
		if ii.IsUp && ii.WiFi.Present {
			wifi = append(wifi, [2]string{fmt.Sprintf("{device=%q}", ii.Name), num(ii.WiFi.SignalDBm)})
		}
	}
	w("wifi_signal_dbm", "Wireless signal level.", "gauge", wifi)

	if b := m.powerSnap.Battery; b.Present && b.Capacity >= 0 {
		w("battery_percent", "Battery charge.", "gauge",
			[][2]string{{"", num(float64(b.Capacity))}})
		if b.PowerW > 0 {
			w("battery_power_watts", "Instantaneous battery power.", "gauge",
				[][2]string{{"", num(b.PowerW)}})
		}
		if b.HealthPct > 0 {
			w("battery_health_percent", "Battery capacity versus design.", "gauge",
				[][2]string{{"", num(b.HealthPct)}})
		}
	}

	if t := m.powerSnap.Throttle; t.Available {
		w("throttled", "Raspberry Pi throttle flags, as live bits.", "gauge", [][2]string{
			{`{flag="under_voltage"}`, boolMetric(t.UnderVoltageNow)},
			{`{flag="throttled"}`, boolMetric(t.ThrottledNow)},
			{`{flag="freq_capped"}`, boolMetric(t.FreqCappedNow)},
			{`{flag="soft_temp_limit"}`, boolMetric(t.SoftTempLimitNow)},
		})
	}

	var wear [][2]string
	for _, h := range m.storage {
		if pct, ok := h.WearPct(); ok {
			wear = append(wear, [2]string{fmt.Sprintf("{device=%q,kind=%q}", h.Device, h.Kind), num(pct)})
		}
	}
	w("storage_wear_percent", "Share of rated write endurance consumed.", "gauge", wear)

	var rtt, loss [][2]string
	for _, r := range m.pings {
		lbl := fmt.Sprintf("{target=%q}", r.Target)
		if r.OK {
			rtt = append(rtt, [2]string{lbl, num(float64(r.RTT.Microseconds()) / 1000)})
		}
		if win := m.lossWin[r.Target]; len(win) > 0 {
			loss = append(loss, [2]string{lbl, num(lossPct(win))})
		}
	}
	w("ping_rtt_milliseconds", "Round-trip time to a monitored target.", "gauge", rtt)
	w("ping_loss_percent", "Packet loss over the rolling window.", "gauge", loss)

	if m.psi.Present {
		var psi [][2]string
		for _, r := range []struct {
			name string
			res  probe.PSIRes
		}{{"cpu", m.psi.CPU}, {"memory", m.psi.Mem}, {"io", m.psi.IO}} {
			if r.res.HasSome {
				psi = append(psi, [2]string{fmt.Sprintf("{resource=%q}", r.name), num(r.res.SomeAvg10)})
			}
		}
		w("pressure_some_avg10", "PSI some-avg10 by resource.", "gauge", psi)
	}

	_, active, _ := m.alerts.summary()
	w("alerts_active", "Alert conditions currently firing.", "gauge",
		[][2]string{{"", strconv.Itoa(active)}})

	return b.String()
}

func boolMetric(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// diskIOFor matches a mount's device to its I/O counters.
//
// The exact name has to be tried first. /proc/diskstats carries a row per
// *partition* as well as per disk, so stripping the partition suffix up front
// gave every partition of a device the whole-disk rate — /boot and /boot/efi
// reported identical, far-too-large figures — and mangled the names that end
// in a device index rather than a partition number: /dev/dm-0 became "dm-",
// /dev/mmcblk0 became "mmcblk", and a root filesystem on LVM or dm-crypt
// reported no disk I/O at all. The stripped base stays as a fallback for the
// kernels and setups that publish only the whole-disk row.
func diskIOFor(ioByDev map[string]probe.DiskIOInfo, dev string) (probe.DiskIOInfo, bool) {
	name := strings.TrimPrefix(dev, "/dev/")
	if io, ok := ioByDev[name]; ok {
		return io, true
	}
	if base := baseDevice(dev); base != name {
		if io, ok := ioByDev[base]; ok {
			return io, true
		}
	}
	return probe.DiskIOInfo{}, false
}

// baseDevice strips /dev/ and a partition suffix, so a partition can fall
// back to the whole disk's counters when its own row is absent. Only the two
// shapes the kernel actually uses are stripped — "p<digits>" after a device
// index (nvme0n1p3, mmcblk0p2) and a trailing digit run after a letter
// (sda1) — because a blanket "drop trailing digits" turns the whole-device
// names dm-0, mmcblk0 and nvme0n1 into devices that do not exist.
func baseDevice(dev string) string {
	d := strings.TrimPrefix(dev, "/dev/")
	if i := strings.LastIndexByte(d, 'p'); i > 0 && i+1 < len(d) &&
		isDigit(d[i-1]) && allDigits(d[i+1:]) {
		return d[:i]
	}
	i := len(d)
	for i > 0 && isDigit(d[i-1]) {
		i--
	}
	if i > 0 && i < len(d) && isLetter(d[i-1]) {
		return d[:i]
	}
	return d
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// topProcs returns the n busiest processes by CPU.
func (m Model) topProcs(n int) []probe.ProcInfo {
	ps := make([]probe.ProcInfo, len(m.procs))
	copy(ps, m.procs)
	sort.Slice(ps, func(i, j int) bool { return ps[i].CPUPct > ps[j].CPUPct })
	if len(ps) > n {
		ps = ps[:n]
	}
	return ps
}
