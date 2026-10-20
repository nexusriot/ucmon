// Package config loads ucmon's optional user configuration from
// $XDG_CONFIG_HOME/ucmon/config.toml (falling back to ~/.config/ucmon/...).
//
// The parser accepts the flat TOML key/value subset (key = value, with #
// comments and optional [section] headers that are ignored). No third-party
// TOML dependency is pulled in, keeping the build pure-Go for cross-compilation.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the resolved settings. Missing keys keep their default.
type Config struct {
	StartTab string // cpu|mem|proc|disk|net|power|gpu

	FastInterval time.Duration // base tick: CPU / memory / network throughput
	ProcInterval time.Duration // process & connection list
	SlowInterval time.Duration // disk, power, GPU/SoC

	TempWarn float64 // °C, yellow threshold
	TempHot  float64 // °C, orange threshold
	TempCrit float64 // °C, red threshold

	// TempExplicit records that the config file set a temperature threshold
	// by hand, which switches off the trip-point calibration below. Without
	// it there is no way to tell "the user wants 80" from "nobody said".
	TempExplicit bool

	// TempFromTripPoints seeds the thresholds from the kernel's own thermal
	// trip points when the config names none. The built-in 50/65/80 suits a
	// Raspberry Pi, whose firmware throttles at 80 °C, and is wrong by tens
	// of degrees on anything else.
	TempFromTripPoints bool

	BacklightStep   int    // percent change per +/- keypress
	BacklightDevice string // optional /sys/class/backlight device override

	// Name globs hidden from their tab, the --once report and the alert
	// engine alike. "*" matches any run of characters, "/" included, so
	// "/snap/*" covers a whole subtree. Empty means show everything.
	NetIgnore  []string // interface names, e.g. veth*, br-*, docker*
	DiskIgnore []string // mount points, e.g. /snap/*, /var/lib/docker/*
	TempIgnore []string // sensor labels, e.g. coretemp_core_*

	// Alerting (Alerts tab + header banner). Thresholds drive edge-triggered
	// alerts; CPU-temperature alerts reuse TempHot (warn) / TempCrit (crit).
	AlertsEnabled  bool    // master switch
	MemWarnPct     float64 // RAM used %% that raises a warning
	SwapWarnPct    float64 // swap used %% that raises a warning
	DiskWarnPct    float64 // filesystem used %% that raises a warning
	BattLowPct     int     // discharging battery %% that raises a warning
	BattCritPct    int     // discharging battery %% that raises a critical alert
	LoadWarnFactor float64 // load1 ≥ factor×cores raises a warning
	AlertLogCap    int     // max alert-log entries retained in memory

	AlertClearTicks int    // consecutive OK ticks before a condition clears
	AlertBell       bool   // terminal bell on a new critical alert
	AlertCmd        string // shell command run on each raise/clear ("" = off)
	AlertLogFile    string // append raise/clear lines to this file ("" = off)

	// WiFi signal alert: warn when a wireless iface signal ≤ this dBm (0 = off).
	WifiLowDBm int

	// Connectivity latency monitor (Network tab).
	PingEnabled     bool    // ping the default gateway on the proc interval
	PingTarget      string  // extra external target (host or IP, "" = none)
	PingLossWarnPct float64 // packet-loss %% that raises a warning (0 = off)

	// PSI pressure alert: warn when some avg10 ≥ this %% for cpu/mem/io (0 = off).
	PSIWarnPct float64

	// Rate-based (predictive) alerts. A level threshold cannot tell a slow
	// drift from a fast one; these fire on the slope, while there is still
	// time to act. 0 disables each.
	BattDrainWarnPctH   float64 // discharge faster than this %%/hour warns
	DiskFullWarnHours   float64 // warn when a filesystem is this many hours from full
	TempRiseWarnCPerMin float64 // warn when a sensor climbs this fast

	// Storage wear + WWAN modem polling.
	ModemEnabled bool // poll ModemManager on the slow interval

	// History and UI constants that were fixed in source.
	HistoryLen    int    // sparkline ring-buffer length
	ProcsCap      int    // process rows rendered
	ReportDir     string // where "w" writes snapshot reports
	HistoryFile   string // persist sparkline history here ("" = off)
	MetricsListen string // Prometheus exporter address, e.g. ":9100" ("" = off)

	// Unknown lists the keys the file set that apply() does not recognise —
	// a typo, or a key from a newer ucmon. They are never fatal (a config
	// written for a later release has to keep working), but silence made a
	// misspelled key indistinguishable from one that had no effect.
	Unknown []UnknownKey
}

// UnknownKey is one unrecognised setting and the line it was written on.
type UnknownKey struct {
	Key  string
	Line int
}

// UnknownKeyNames lists the unrecognised keys in the order they appeared,
// for a one-line notice.
func (c Config) UnknownKeyNames() []string {
	out := make([]string, 0, len(c.Unknown))
	for _, u := range c.Unknown {
		out = append(out, u.Key)
	}
	return out
}

// Default returns the built-in configuration used when no file is present.
func Default() Config {
	return Config{
		StartTab:      "cpu",
		FastInterval:  1 * time.Second,
		ProcInterval:  3 * time.Second,
		SlowInterval:  5 * time.Second,
		TempWarn:      50,
		TempHot:       65,
		TempCrit:      80,
		BacklightStep: 10,

		TempFromTripPoints: true,

		AlertsEnabled:  true,
		MemWarnPct:     90,
		SwapWarnPct:    50,
		DiskWarnPct:    90,
		BattLowPct:     15,
		BattCritPct:    5,
		LoadWarnFactor: 2.0,
		AlertLogCap:    200,

		AlertClearTicks: 3,
		AlertBell:       true,

		PingEnabled: true,

		BattDrainWarnPctH:   25,
		DiskFullWarnHours:   6,
		TempRiseWarnCPerMin: 3,

		HistoryLen: 200,
		ProcsCap:   200,
	}
}

// Path returns the location ucmon looks for its config file.
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ucmon", "config.toml")
}

// Load reads the config file at Path(). A missing file is not an error: the
// defaults are returned. Malformed individual lines are skipped silently so a
// typo never prevents the monitor from starting.
func Load() Config {
	return LoadFrom(Path())
}

// LoadFrom reads the config file at an explicit path (the --config flag).
func LoadFrom(path string) Config {
	cfg := Default()
	if path == "" {
		return cfg
	}
	f, err := os.Open(path)
	if err != nil {
		return cfg
	}
	defer f.Close()

	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for n := 0; sc.Scan(); {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") { // [section] headers are ignored
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		if apply(&cfg, key, splitValue(line[eq+1:])) {
			continue
		}
		// A line with nothing before the "=" is malformed, not a key, and
		// naming it "" in a report would explain nothing. Repeats are
		// recorded once, at the line that first introduced them.
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		cfg.Unknown = append(cfg.Unknown, UnknownKey{Key: key, Line: n})
	}
	return cfg
}

// splitValue turns the raw text after `=` into the value: it drops a trailing
// `#` comment that is not inside quotes, then removes one matched pair of
// surrounding quotes. Quotes belonging to the value survive, so a shell hook
// can be written as a TOML literal string:
//
//	alert_cmd = 'notify-send "ucmon" "$UCMON_ALERT_MSG"'   # fires on each event
//
// Escape sequences are not interpreted; a backslash only shields the next
// character from ending the quoted stretch.
func splitValue(raw string) string {
	val := raw
	var quote byte
scan:
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; {
		case quote != 0 && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case quote == 0 && c == '#':
			val = raw[:i]
			break scan
		}
	}

	val = strings.TrimSpace(val)
	if n := len(val); n >= 2 {
		if q := val[0]; (q == '"' || q == '\'') && val[n-1] == q {
			val = val[1 : n-1]
		}
	}
	return val
}

// apply sets the field key names, and reports whether the key was one it
// knows. Everything LoadFrom could not place comes back false, so an
// unrecognised key can be named rather than silently dropped.
func apply(cfg *Config, key, val string) bool {
	switch key {
	case "start_tab", "starttab", "tab":
		cfg.StartTab = strings.ToLower(val)
	case "fast_interval", "fastinterval", "cpu_interval":
		if d, ok := parseInterval(val); ok {
			cfg.FastInterval = d
		}
	case "proc_interval", "procinterval", "process_interval":
		if d, ok := parseInterval(val); ok {
			cfg.ProcInterval = d
		}
	case "slow_interval", "slowinterval", "disk_interval":
		if d, ok := parseInterval(val); ok {
			cfg.SlowInterval = d
		}
	case "temp_warn", "tempwarn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempWarn = f
			cfg.TempExplicit = true
		}
	case "temp_hot", "temphot":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempHot = f
			cfg.TempExplicit = true
		}
	case "temp_crit", "tempcrit":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempCrit = f
			cfg.TempExplicit = true
		}
	case "temp_from_trip_points", "tempfromtrippoints", "temp_auto":
		cfg.TempFromTripPoints = parseBool(val, cfg.TempFromTripPoints)
	case "backlight_step", "backlightstep":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BacklightStep = n
		}
	case "backlight_device", "backlightdevice":
		cfg.BacklightDevice = val
	case "net_ignore", "netignore", "iface_ignore":
		cfg.NetIgnore = splitList(val)
	case "disk_ignore", "diskignore", "mount_ignore":
		cfg.DiskIgnore = splitList(val)
	case "temp_ignore", "tempignore", "sensor_ignore":
		cfg.TempIgnore = splitList(val)
	case "alerts_enabled", "alertsenabled", "alerts":
		cfg.AlertsEnabled = parseBool(val, cfg.AlertsEnabled)
	case "mem_warn_pct", "memwarnpct", "mem_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.MemWarnPct = f
		}
	case "swap_warn_pct", "swapwarnpct", "swap_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.SwapWarnPct = f
		}
	case "disk_warn_pct", "diskwarnpct", "disk_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.DiskWarnPct = f
		}
	case "batt_low_pct", "battlowpct", "batt_low":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BattLowPct = n
		}
	case "batt_crit_pct", "battcritpct", "batt_crit":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BattCritPct = n
		}
	case "load_warn_factor", "loadwarnfactor", "load_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.LoadWarnFactor = f
		}
	case "alert_log_cap", "alertlogcap":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AlertLogCap = n
		}
	case "alert_clear_ticks", "alertclearticks":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.AlertClearTicks = n
		}
	case "alert_bell", "alertbell":
		cfg.AlertBell = parseBool(val, cfg.AlertBell)
	case "alert_cmd", "alertcmd":
		cfg.AlertCmd = val
	case "alert_log_file", "alertlogfile":
		cfg.AlertLogFile = expandHome(val)
	case "wifi_low_dbm", "wifilowdbm", "wifi_low":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.WifiLowDBm = n
		}
	case "ping_enabled", "pingenabled", "ping":
		cfg.PingEnabled = parseBool(val, cfg.PingEnabled)
	case "ping_target", "pingtarget":
		cfg.PingTarget = val
	case "ping_loss_warn_pct", "pinglosswarnpct", "ping_loss_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.PingLossWarnPct = f
		}
	case "psi_warn_pct", "psiwarnpct", "psi_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.PSIWarnPct = f
		}
	case "batt_drain_warn_pct_h", "battdrainwarnpcth", "batt_drain_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.BattDrainWarnPctH = f
		}
	case "disk_full_warn_hours", "diskfullwarnhours", "disk_full_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.DiskFullWarnHours = f
		}
	case "temp_rise_warn_c_per_min", "temprisewarncpermin", "temp_rise_warn":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			cfg.TempRiseWarnCPerMin = f
		}
	case "modem_enabled", "modemenabled", "modem":
		cfg.ModemEnabled = parseBool(val, cfg.ModemEnabled)
	case "history_len", "historylen", "spark_history":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.HistoryLen = n
		}
	case "procs_cap", "procscap", "proc_display_cap":
		if n, err := strconv.Atoi(val); err == nil {
			cfg.ProcsCap = n
		}
	case "report_dir", "reportdir", "snapshot_dir":
		cfg.ReportDir = expandHome(val)
	case "history_file", "historyfile":
		cfg.HistoryFile = expandHome(val)
	case "metrics_listen", "metricslisten", "metrics":
		cfg.MetricsListen = val
	default:
		return false
	}
	return true
}

// expandHome resolves a leading "~/" against the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path[1:], "/"))
		}
	}
	return path
}

// parseBool accepts the common truthy/falsy spellings; an unrecognized value
// keeps the supplied default.
func parseBool(val string, def bool) bool {
	switch strings.ToLower(val) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// parseInterval accepts a Go duration ("500ms", "2s") or a bare number that is
// interpreted as seconds ("3", "1.5").
func parseInterval(val string) (time.Duration, bool) {
	if d, err := time.ParseDuration(val); err == nil && d > 0 {
		return d, true
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
		return time.Duration(f * float64(time.Second)), true
	}
	return 0, false
}

// Sanitize clamps the intervals to sane lower bounds so a bad config can't
// peg the CPU with a sub-millisecond tick.
func (c *Config) Sanitize() {
	if c.FastInterval < 200*time.Millisecond {
		c.FastInterval = 200 * time.Millisecond
	}
	if c.ProcInterval < c.FastInterval {
		c.ProcInterval = c.FastInterval
	}
	if c.SlowInterval < c.FastInterval {
		c.SlowInterval = c.FastInterval
	}
	if c.BacklightStep < 1 {
		c.BacklightStep = 1
	}
	if c.BacklightStep > 100 {
		c.BacklightStep = 100
	}
	if c.AlertLogCap < 16 {
		c.AlertLogCap = 16
	}
	if c.LoadWarnFactor <= 0 {
		c.LoadWarnFactor = 2.0
	}

	// Alert thresholds are compared against 0–100 readings, so an out-of-range
	// value silently either never fires or fires on every tick. A typo like
	// "900" or "-5" falls back to the default instead.
	def := Default()
	c.MemWarnPct = pctOr(c.MemWarnPct, def.MemWarnPct)
	c.SwapWarnPct = pctOr(c.SwapWarnPct, def.SwapWarnPct)
	c.DiskWarnPct = pctOr(c.DiskWarnPct, def.DiskWarnPct)

	// Battery tiers allow 0 (fire only at a flat pack) and stay ordered.
	c.BattLowPct = clampPctInt(c.BattLowPct)
	c.BattCritPct = clampPctInt(c.BattCritPct)
	if c.BattCritPct > c.BattLowPct {
		c.BattCritPct = c.BattLowPct
	}

	// TempColor and the alert engine both assume warn ≤ hot ≤ crit. Push the
	// lower tiers down rather than sorting, so explicitly lowering temp_crit
	// on a passively cooled board keeps the value the user asked for.
	if c.TempHot > c.TempCrit {
		c.TempHot = c.TempCrit
	}
	if c.TempWarn > c.TempHot {
		c.TempWarn = c.TempHot
	}
	if c.AlertClearTicks < 1 {
		c.AlertClearTicks = 1
	}
	if c.AlertClearTicks > 60 {
		c.AlertClearTicks = 60
	}
	// A positive dBm threshold is meaningless (signal levels are negative).
	if c.WifiLowDBm > 0 {
		c.WifiLowDBm = -c.WifiLowDBm
	}
	if c.PingLossWarnPct < 0 {
		c.PingLossWarnPct = 0
	}
	if c.PingLossWarnPct > 100 {
		c.PingLossWarnPct = 100
	}
	if c.PSIWarnPct < 0 {
		c.PSIWarnPct = 0
	}
	if c.PSIWarnPct > 100 {
		c.PSIWarnPct = 100
	}

	// A negative rate threshold would fire on every reading; 0 means off.
	if c.BattDrainWarnPctH < 0 {
		c.BattDrainWarnPctH = 0
	}
	if c.DiskFullWarnHours < 0 {
		c.DiskFullWarnHours = 0
	}
	if c.TempRiseWarnCPerMin < 0 {
		c.TempRiseWarnCPerMin = 0
	}

	// History shorter than a sparkline is wide draws nothing; absurdly long
	// costs memory per interface, sensor and core for no visible gain.
	if c.HistoryLen < 10 {
		c.HistoryLen = 10
	}
	if c.HistoryLen > 5000 {
		c.HistoryLen = 5000
	}
	if c.ProcsCap < 10 {
		c.ProcsCap = 10
	}
	if c.ProcsCap > 10000 {
		c.ProcsCap = 10000
	}
}

// pctOr returns v when it is a usable percentage threshold and def otherwise.
// 0 is rejected: no reading is ever below it, so it would alert forever.
func pctOr(v, def float64) float64 {
	if v <= 0 || v > 100 {
		return def
	}
	return v
}

// clampPctInt bounds a whole-percent threshold to 0–100.
func clampPctInt(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
