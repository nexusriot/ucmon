package probe

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// mmcliPaths are the absolute locations checked before PATH, because a
// non-root PATH often lacks the sbin dirs. A var so tests can point it at a
// fake tree, matching iwPaths.
var mmcliPaths = []string{"/usr/bin/mmcli", "/usr/sbin/mmcli", "/bin/mmcli"}

// ModemInfo describes a WWAN modem as ModemManager reports it — the uConsole
// 4G extension board, or any USB/PCIe modem on a Pi.
type ModemInfo struct {
	Present      bool
	Model        string
	Manufacturer string
	State        string // "connected", "registered", "searching", "disabled", …
	FailedReason string
	Operator     string
	OperatorCode string
	AccessTech   string // "lte", "umts", "5gnr", …
	SignalPct    int    // ModemManager's own 0-100 quality, -1 when unknown
	SignalRecent bool   // quality was measured now rather than cached
	RSSIdBm      float64
	HasRSSI      bool
	IMEI         string
	Bands        string
}

// Registered reports whether the modem is on a network at all.
func (m ModemInfo) Registered() bool {
	switch m.State {
	case "connected", "registered", "connecting", "disconnecting":
		return true
	}
	return false
}

func mmcliPath() string {
	for _, p := range mmcliPaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("mmcli"); err == nil {
		return p
	}
	return ""
}

// ReadModem returns the first modem ModemManager knows about. A host without
// mmcli, without ModemManager running, or without a modem yields
// Present=false rather than an error — the uConsole ships both with and
// without the 4G board.
func ReadModem() ModemInfo {
	bin := mmcliPath()
	if bin == "" {
		return ModemInfo{}
	}

	list, ok := runMmcli(bin, "--list-modems")
	if !ok {
		return ModemInfo{}
	}
	idx, ok := firstModemIndex(list)
	if !ok {
		return ModemInfo{}
	}

	out, ok := runMmcli(bin, "--modem", idx)
	if !ok {
		return ModemInfo{}
	}
	info := parseModem(out)
	if !info.Present {
		return ModemInfo{}
	}

	// Signal quality in the summary is often a cached value; --signal-get
	// asks the modem for a fresh RSSI when signal polling is enabled.
	if sig, ok := runMmcli(bin, "--modem", idx, "--signal-get"); ok {
		if rssi, ok := parseSignalRSSI(sig); ok {
			info.RSSIdBm, info.HasRSSI = rssi, true
		}
	}
	return info
}

func runMmcli(bin string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// firstModemIndex pulls a modem index out of `mmcli --list-modems`, whose
// lines look like "/org/freedesktop/ModemManager1/Modem/0 [QUALCOMM] EC25".
func firstModemIndex(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		i := strings.Index(line, "/Modem/")
		if i < 0 {
			continue
		}
		rest := line[i+len("/Modem/"):]
		end := 0
		for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
			end++
		}
		if end > 0 {
			return rest[:end], true
		}
	}
	return "", false
}

// parseModem reads the human-readable `mmcli --modem N` output. The key/value
// layout ("  |     state: connected") is stable across ModemManager 1.1x and
// avoids depending on the KEY=VALUE output mode, which older builds lack.
func parseModem(s string) ModemInfo {
	m := ModemInfo{SignalPct: -1}
	for _, raw := range strings.Split(s, "\n") {
		key, val, ok := splitMmcliLine(raw)
		if !ok {
			continue
		}

		switch key {
		case "model":
			m.Model, m.Present = val, true
		case "manufacturer":
			m.Manufacturer, m.Present = val, true
		case "state":
			m.State, m.Present = strings.ToLower(firstWord(val)), true
		case "failed reason":
			m.FailedReason = val
		case "operator name":
			m.Operator = val
		case "operator id", "operator code":
			m.OperatorCode = val
		case "access tech", "access technology", "current":
			if m.AccessTech == "" && looksLikeAccessTech(val) {
				m.AccessTech = strings.ToLower(val)
			}
		case "signal quality":
			if pct, recent, ok := parseSignalQuality(val); ok {
				m.SignalPct, m.SignalRecent = pct, recent
			}
		case "imei", "equipment id":
			m.IMEI = val
		case "bands":
			m.Bands = val
		}
	}
	if !m.Present {
		return ModemInfo{}
	}
	return m
}

// splitMmcliLine pulls the key and value out of one line of mmcli's
// human-readable output. The layout nests a section name and the key in the
// same row — "  Status   |    state: connected" — so the key is whatever sits
// between the last column rule and the colon, not the whole left-hand side.
func splitMmcliLine(raw string) (key, val string, ok bool) {
	line := strings.TrimSpace(raw)
	key, val, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	if i := strings.LastIndex(key, "|"); i >= 0 {
		key = key[i+1:]
	}
	key = strings.TrimSpace(strings.ToLower(strings.Trim(key, "-| ")))
	val = strings.TrimSpace(val)
	if key == "" || val == "" || val == "--" {
		return "", "", false
	}
	return key, val, true
}

// looksLikeAccessTech keeps the "current:" key — which ModemManager reuses for
// bands and modes — from being mistaken for the radio technology.
func looksLikeAccessTech(val string) bool {
	switch strings.ToLower(firstWord(val)) {
	case "lte", "umts", "gsm", "hspa", "hspa+", "evdo", "5gnr", "edge", "gprs":
		return true
	}
	return false
}

// parseSignalQuality reads `85% (recent)`.
func parseSignalQuality(val string) (pct int, recent bool, ok bool) {
	recent = strings.Contains(val, "recent")
	num, _, _ := strings.Cut(val, "%")
	n, err := strconv.Atoi(strings.TrimSpace(firstWord(num)))
	if err != nil || n < 0 || n > 100 {
		return 0, false, false
	}
	return n, recent, true
}

// parseSignalRSSI pulls the rssi line out of `mmcli --signal-get`:
//
//	| rssi: -73.00 dBm
func parseSignalRSSI(s string) (float64, bool) {
	for _, raw := range strings.Split(s, "\n") {
		key, val, ok := splitMmcliLine(raw)
		if !ok || key != "rssi" {
			continue
		}
		v, err := strconv.ParseFloat(firstWord(val), 64)
		if err != nil || v == 0 {
			continue
		}
		return v, true
	}
	return 0, false
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}
