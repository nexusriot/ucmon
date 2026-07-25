package probe

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// wirelessPath is a var so tests can point it at a fake file.
var wirelessPath = "/proc/net/wireless"

// WiFiInfo holds the wireless link statistics for one interface, parsed from
// /proc/net/wireless. Pure file read — no external tooling or cgo required.
type WiFiInfo struct {
	Present     bool
	LinkQuality float64 // raw link quality (driver scale, typically 0–70)
	QualityMax  float64 // assumed maximum for the quality scale
	QualityPct  float64 // LinkQuality / QualityMax * 100, clamped 0–100
	SignalDBm   float64 // signal level in dBm (negative; closer to 0 is stronger)
}

// readWirelessStats parses /proc/net/wireless and returns one entry per
// wireless interface keyed by interface name. On non-wireless hosts the file
// is absent or empty and an empty map is returned.
//
// File format (columns after the two header lines):
//
//	Inter-| sta-|   Quality        |   Discarded packets ...
//	 face | tus | link level noise |  nwid  crypt ...
//	 wlan0: 0000   70.  -40.  -256        0 ...
func readWirelessStats() map[string]WiFiInfo {
	out := map[string]WiFiInfo{}

	f, err := os.Open(wirelessPath)
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		if lineNo <= 2 { // skip the two header rows
			continue
		}
		line := strings.TrimSpace(sc.Text())
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 3 {
			continue
		}

		// fields: status, link-quality, signal-level, [noise], ...
		quality := parseWirelessNum(fields[1])
		signal := parseWirelessNum(fields[2])

		const qualityMax = 70.0 // conventional max for the cfg80211 quality scale
		pct := quality / qualityMax * 100
		if pct < 0 {
			pct = 0
		}
		if pct > 100 {
			pct = 100
		}

		out[name] = WiFiInfo{
			Present:     true,
			LinkQuality: quality,
			QualityMax:  qualityMax,
			QualityPct:  pct,
			SignalDBm:   signal,
		}
	}
	return out
}

// parseWirelessNum strips the trailing '.' that /proc/net/wireless appends to
// the quality/level/noise columns and parses the value.
func parseWirelessNum(s string) float64 {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// IWLink holds the association details `iw dev <iface> link` reports —
// things /proc/net/wireless cannot provide (SSID, channel, bitrates).
type IWLink struct {
	Present   bool
	SSID      string
	FreqMHz   int
	Channel   int
	SignalDBm int
	TxMbps    float64
	RxMbps    float64
}

// iwBinPath locates the iw binary; sbin dirs are checked explicitly because
// non-root PATHs often lack them.
func iwBinPath() string {
	for _, p := range []string{"/usr/sbin/iw", "/sbin/iw", "/usr/bin/iw"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("iw"); err == nil {
		return p
	}
	return ""
}

// ReadIWLinks runs `iw dev <iface> link` for every wireless interface found
// in /proc/net/wireless. A missing iw binary (or no wireless hardware)
// yields an empty map — same graceful-absence pattern as vcgencmd.
func ReadIWLinks() map[string]IWLink {
	out := map[string]IWLink{}
	bin := iwBinPath()
	if bin == "" {
		return out
	}
	for name := range readWirelessStats() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		b, err := exec.CommandContext(ctx, bin, "dev", name, "link").Output()
		cancel()
		if err != nil {
			continue
		}
		if l := parseIWLink(string(b)); l.Present {
			out[name] = l
		}
	}
	return out
}

// parseIWLink parses `iw dev <iface> link` output:
//
//	Connected to aa:bb:cc:dd:ee:ff (on wlan0)
//	        SSID: MyNet
//	        freq: 5180
//	        signal: -52 dBm
//	        rx bitrate: 433.3 MBit/s VHT-MCS 9 80MHz short GI VHT-NSS 1
//	        tx bitrate: 433.3 MBit/s ...
func parseIWLink(s string) IWLink {
	var l IWLink
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Connected to "):
			l.Present = true
		case strings.HasPrefix(line, "SSID:"):
			l.SSID = strings.TrimSpace(strings.TrimPrefix(line, "SSID:"))
		case strings.HasPrefix(line, "freq:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "freq:"))
			if f, err := strconv.ParseFloat(v, 64); err == nil { // newer iw prints "5180.0"
				l.FreqMHz = int(f)
				l.Channel = FreqToChannel(l.FreqMHz)
			}
		case strings.HasPrefix(line, "signal:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "signal:"))
			v = strings.TrimSpace(strings.TrimSuffix(v, "dBm"))
			if n, err := strconv.Atoi(v); err == nil {
				l.SignalDBm = n
			}
		case strings.HasPrefix(line, "tx bitrate:"):
			l.TxMbps = parseBitrate(strings.TrimPrefix(line, "tx bitrate:"))
		case strings.HasPrefix(line, "rx bitrate:"):
			l.RxMbps = parseBitrate(strings.TrimPrefix(line, "rx bitrate:"))
		}
	}
	return l
}

// parseBitrate extracts the leading MBit/s figure from an iw bitrate line.
func parseBitrate(s string) float64 {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return v
}

// FreqToChannel maps a WiFi center frequency (MHz) to its channel number.
// Unknown frequencies return 0.
func FreqToChannel(mhz int) int {
	switch {
	case mhz == 2484:
		return 14
	case mhz >= 2412 && mhz < 2484:
		return (mhz - 2407) / 5
	case mhz >= 5955 && mhz <= 7115: // 6 GHz band
		return (mhz - 5950) / 5
	case mhz >= 5000 && mhz < 5950:
		return (mhz - 5000) / 5
	default:
		return 0
	}
}
