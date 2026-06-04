package probe

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

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

	f, err := os.Open("/proc/net/wireless")
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
